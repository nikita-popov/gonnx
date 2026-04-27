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
//	pull    <name>              download bundle assets (with progress bar)
//	list                        list installed bundles
//	get <name>                  show bundle details
//	rm  <name>                  uninstall a bundle
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
		c.do(http.MethodDelete, "/v1/models/"+args[1], nil)
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
//
// flag.FlagSet stops at the first non-flag argument, so flags after the URL
// would be silently ignored. We partition args ourselves first.
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

// pullCmd streams the NDJSON progress from the daemon and renders an
// Ollama-style progress bar in the terminal.
//
// Output example (one overwritten line per asset):
//
//	model   [=============>       ]  66%  221.2 MB / 334.1 MB  14.3 MB/s
//
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

	// Each active asset gets one terminal line; we track per-asset state
	// so we can overwrite the correct line with \r.
	type assetState struct {
		written  int64
		total    int64
		startAt  time.Time
		lastLine int // length of last printed line (for padding)
	}
	states := map[string]*assetState{}
	// order preserves insertion order for stable output
	order := []string{}

	// Track how many lines we've printed so we can move cursor up.
	printedLines := 0

	clearLines := func(n int) {
		for i := 0; i < n; i++ {
			fmt.Fprint(os.Stderr, "\033[1A\033[2K") // cursor up + erase line
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

		case "done":
			// Final render with all bars at 100%.
			for _, id := range order {
				st := states[id]
				if st.total > 0 {
					st.written = st.total
				}
			}
			renderAll()
			// Print newlines to leave bars on screen.
			for range order {
				fmt.Fprintln(os.Stderr)
			}
			skipped, _ := ev["skipped"].(bool)
			if skipped {
				fmt.Fprintf(os.Stderr, "%s: all assets already present\n", name)
			} else {
				fmt.Fprintf(os.Stderr, "%s: pull complete\n", name)
			}
			return

		case "error":
			// Print newlines to leave partial bars visible.
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

// formatBar renders a single progress bar line for one asset.
//
//	model   [=============>       ]  66%  221.2 MB / 334.1 MB  14.3 MB/s
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
		// Unknown total: spinning indicator.
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

// throughput formats bytes/sec as a human-readable string.
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

// int64AsFloat extracts a numeric JSON value as int64 (json unmarshals numbers
// as float64 by default).
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

// --- HTTP client -----------------------------------------------------------

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

// --- helpers ---------------------------------------------------------------

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
  pull    <name>                download bundle assets (progress bar)
  list                          list installed bundles
  get <name>                    show bundle metadata
  rm  <name>                    uninstall bundle
  load   <name>                 start worker process
  unload <name>                 stop worker process
  describe <name>               show input/output schema
  run <name> [json]             run inference (reads stdin if no json arg)

Flags for install:
  --name   override bundle name (default: name from manifest)
  --ref    override git ref     (default: master)
  --dir    subdir inside repo that contains the bundle

Environment:
  GONNX_HOST   daemon address (default http://localhost:7860)`)
}
