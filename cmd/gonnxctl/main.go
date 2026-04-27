// gonnxctl is the command-line client for gonnxd.
//
// Usage:
//
//	gonnxctl [--host http://localhost:7860] <command> [args]
//
// Commands:
//
//	healthz                     check daemon liveness
//	install <source> [--name N] install a bundle
//	update  <name>              re-install bundle from same source (refreshes manifest)
//	pull    <name>              download bundle assets (with progress bar)
//	list                        list installed bundles
//	get <name>                  show bundle details
//	rm  <name>                  uninstall a bundle (stops worker + removes files)
//	load   <name>               start worker
//	unload <name>               stop worker
//	describe <name>             show input/output schema
//	run <name> [json-body]      run inference (reads stdin if no arg)
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func main() {
	host := flag.String("host", envOr("GONNX_HOST", "http://localhost:7860"), "gonnxd address")
	flag.Usage = usage
	flag.Parse()

	args := flag.Args()
	if len(args) == 0 {
		usage()
		os.Exit(1)
	}

	c := &client{base: strings.TrimRight(*host, "/")}

	switch args[0] {
	case "healthz":
		c.get("/v1/healthz")
	case "install":
		installCmd(c, args[1:])
	case "update":
		requireArg(args, 2, "update <name>")
		updateCmd(c, args[1])
	case "pull":
		requireArg(args, 2, "pull <name>")
		pullCmd(c, args[1])
	case "list":
		c.get("/v1/models")
	case "get":
		requireArg(args, 2, "get <name>")
		c.get("/v1/models/" + args[1])
	case "rm":
		requireArg(args, 2, "rm <name>")
		rmCmd(c, args[1])
	case "load":
		requireArg(args, 2, "load <name>")
		c.post("/v1/models/"+args[1]+":load", nil)
	case "unload":
		requireArg(args, 2, "unload <name>")
		c.post("/v1/models/"+args[1]+":unload", nil)
	case "describe":
		requireArg(args, 2, "describe <name>")
		c.get("/v1/models/" + args[1] + ":describe")
	case "run":
		requireArg(args, 2, "run <name> [json]")
		runCmd(c, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", args[0])
		os.Exit(1)
	}
}

// installCmd handles: gonnxctl install [flags] <url>
func installCmd(c *client, args []string) {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	name := fs.String("name", "", "override bundle name")
	ref := fs.String("ref", "", "override git ref")
	dir := fs.String("dir", "", "override bundle subdir")

	var flagTokens, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			flagTokens = append(flagTokens, a)
			isKV := strings.Contains(a, "=")
			if !isKV && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				flagTokens = append(flagTokens, args[i])
			}
		} else {
			positional = append(positional, a)
		}
	}
	fs.Parse(flagTokens) //nolint:errcheck

	if len(positional) == 0 {
		fmt.Fprintln(os.Stderr, "install requires a source URL")
		os.Exit(1)
	}
	body := map[string]string{
		"source": positional[0],
		"name":   *name,
		"ref":    *ref,
		"dir":    *dir,
	}
	c.post("/v1/models:install", body)
}

// updateCmd re-installs a bundle from the same source recorded in the registry.
func updateCmd(c *client, name string) {
	raw, err := c.raw(http.MethodGet, "/v1/models/"+name, nil, "")
	if err != nil {
		fatal(fmt.Errorf("get %s: %w", name, err))
	}
	var meta struct {
		SourceURL string `json:"sourceUrl"`
		SourceRef string `json:"sourceRef"`
		SourceDir string `json:"sourceDir"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		fatal(fmt.Errorf("parse metadata: %w", err))
	}
	if meta.SourceURL == "" {
		fatal(fmt.Errorf("bundle %q has no recorded source URL", name))
	}
	body := map[string]string{
		"source": meta.SourceURL,
		"name":   name,
		"ref":    meta.SourceRef,
		"dir":    meta.SourceDir,
	}
	c.post("/v1/models:install", body)
}

// rmCmd stops the worker, removes the registry entry, and deletes bundle files.
func rmCmd(c *client, name string) {
	_, err := c.raw(http.MethodDelete, "/v1/models/"+name, nil, "")
	if err != nil {
		fatal(err)
	}
	fmt.Fprintf(os.Stderr, "%s: removed\n", name)
}

// pullCmd streams the NDJSON progress from the daemon and renders a progress bar.
func pullCmd(c *client, name string) {
	req, err := http.NewRequest(http.MethodPost,
		c.base+"/v1/models/"+name+":pull", nil)
	if err != nil {
		fatal(err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fatal(fmt.Errorf("request failed: %w", err))
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		fatal(fmt.Errorf("HTTP %d: %s", resp.StatusCode, body))
	}

	type assetState struct {
		written  int64
		total    int64
		startAt  time.Time
		lastLine int
	}
	states := map[string]*assetState{}
	order := []string{}
	printedLines := 0

	clearLines := func(n int) {
		for i := 0; i < n; i++ {
			fmt.Fprint(os.Stderr, "\033[1A\033[2K")
		}
	}

	renderAll := func() {
		if printedLines > 0 {
			clearLines(printedLines)
		}
		printedLines = 0
		for _, id := range order {
			st := states[id]
			line := formatBar(id, st.written, st.total, time.Since(st.startAt))
			fmt.Fprintf(os.Stderr, "%s\n", line)
			printedLines++
		}
	}

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		var ev map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			continue
		}

		switch ev["status"] {
		case "pulling":
			id, _ := ev["asset"].(string)
			written := int64AsFloat(ev["written"])
			total := int64AsFloat(ev["total"])

			st, ok := states[id]
			if !ok {
				st = &assetState{startAt: time.Now()}
				states[id] = st
				order = append(order, id)
			}
			st.written = written
			st.total = total
			renderAll()

		case "venv":
			// Print venv status lines below the asset bars.
			if printedLines > 0 {
				clearLines(printedLines)
				printedLines = 0
			}
			for _, id := range order {
				st := states[id]
				if st.total > 0 {
					st.written = st.total
				}
			}
			renderAll()
			msg, _ := ev["msg"].(string)
			fmt.Fprintf(os.Stderr, "venv: %s\n", msg)
			printedLines++

		case "done":
			for _, id := range order {
				st := states[id]
				if st.total > 0 {
					st.written = st.total
				}
			}
			renderAll()
			for range order {
				fmt.Fprintln(os.Stderr)
			}
			fmt.Fprintf(os.Stderr, "%s: pull complete\n", name)
			return

		case "error":
			for range order {
				fmt.Fprintln(os.Stderr)
			}
			msg, _ := ev["error"].(string)
			fatal(fmt.Errorf("pull failed: %s", msg))
		}
	}
	if err := scanner.Err(); err != nil {
		fatal(fmt.Errorf("stream read: %w", err))
	}
}

func formatBar(id string, written, total int64, elapsed time.Duration) string {
	const barWidth = 20

	var pct float64
	var bar string
	if total > 0 {
		pct = float64(written) / float64(total) * 100
		filled := int(pct / 100 * barWidth)
		if filled > barWidth {
			filled = barWidth
		}
		empty := barWidth - filled
		headPos := filled - 1
		barRunes := make([]byte, barWidth)
		for i := range barRunes {
			switch {
			case i < headPos:
				barRunes[i] = '='
			case i == headPos && filled > 0 && filled < barWidth:
				barRunes[i] = '>'
			case i < filled:
				barRunes[i] = '='
			default:
				barRunes[i] = ' '
			}
			_ = empty
		}
		bar = string(barRunes)
	} else {
		spinners := []string{"-", "\\", "|", "/"}
		spin := spinners[(int(elapsed.Seconds()*4))%4]
		bar = fmt.Sprintf("%-*s", barWidth, spin)
		pct = 0
	}

	speed := throughput(written, elapsed)
	writtenMB := float64(written) / 1e6
	totalMB := float64(total) / 1e6

	if total > 0 {
		return fmt.Sprintf("%-10s [%s] %3.0f%%  %.1f MB / %.1f MB  %s",
			id, bar, pct, writtenMB, totalMB, speed)
	}
	return fmt.Sprintf("%-10s [%s]  %.1f MB  %s",
		id, bar, writtenMB, speed)
}

func throughput(bytes int64, elapsed time.Duration) string {
	if elapsed < 100*time.Millisecond || bytes == 0 {
		return "--"
	}
	bps := float64(bytes) / elapsed.Seconds()
	switch {
	case bps >= 1e9:
		return fmt.Sprintf("%.1f GB/s", bps/1e9)
	case bps >= 1e6:
		return fmt.Sprintf("%.1f MB/s", bps/1e6)
	default:
		return fmt.Sprintf("%.1f KB/s", bps/1e3)
	}
}

func int64AsFloat(v any) int64 {
	f, _ := v.(float64)
	return int64(f)
}

func runCmd(c *client, args []string) {
	name := args[0]
	var body []byte
	if len(args) > 1 {
		body = []byte(args[1])
	} else {
		var err error
		body, err = io.ReadAll(os.Stdin)
		if err != nil {
			fatal(err)
		}
	}
	resp, err := c.raw(http.MethodPost, "/v1/models/"+name+":predict",
		bytes.NewReader(body), "application/json")
	if err != nil {
		fatal(err)
	}
	prettyPrint(resp)
}

type client struct{ base string }

func (c *client) get(path string) {
	body, err := c.raw(http.MethodGet, path, nil, "")
	if err != nil {
		fatal(err)
	}
	prettyPrint(body)
}

func (c *client) post(path string, v any) {
	body, err := c.raw(http.MethodPost, path, jsonBody(v), "application/json")
	if err != nil {
		fatal(err)
	}
	prettyPrint(body)
}

func (c *client) do(method, path string, v any) {
	var r io.Reader
	if v != nil {
		r = jsonBody(v)
	}
	body, err := c.raw(method, path, r, "application/json")
	if err != nil {
		fatal(err)
	}
	if len(body) > 0 {
		prettyPrint(body)
	}
}

func (c *client) raw(method, path string, body io.Reader, ct string) ([]byte, error) {
	req, err := http.NewRequest(method, c.base+path, body)
	if err != nil {
		return nil, err
	}
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	out, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, out)
	}
	return out, nil
}

func jsonBody(v any) io.Reader {
	if v == nil {
		return strings.NewReader("{}")
	}
	b, _ := json.Marshal(v)
	return bytes.NewReader(b)
}

func prettyPrint(b []byte) {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		fmt.Println(string(b))
		return
	}
	out, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(out))
}

func requireArg(args []string, n int, usage string) {
	if len(args) < n {
		fmt.Fprintf(os.Stderr, "usage: gonnxctl %s\n", usage)
		os.Exit(1)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

func usage() {
	fmt.Fprintln(os.Stderr, `gonnxctl — gonnx daemon client

Usage:
  gonnxctl [--host URL] <command> [args]

Commands:
  healthz                       check daemon liveness
  install <src> [--name N]      install bundle from git source
  update  <name>                re-install from same source (refreshes manifest)
  pull    <name>                download bundle assets + setup venv
  list                          list installed bundles
  get <name>                    show bundle metadata
  rm  <name>                    uninstall bundle (stops worker, removes files)
  load   <name>                 start worker process
  unload <name>                 stop worker process
  describe <name>               show input/output schema
  run <name> [json]             run inference (reads stdin if no json arg)

Flags for install:
  --name   override bundle name (default: name from manifest)
  --ref    override git ref     (default: master)
  --dir    subdir inside repo that contains the bundle

Environment:
  GONNX_HOST     daemon address      (default http://localhost:7860)
  GONNXD_SDK_DIR path to sdk/python  (auto-detected from binary location)`)
}
