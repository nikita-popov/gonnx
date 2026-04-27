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
//	list                        list installed bundles
//	get <name>                  show bundle details
//	rm  <name>                  uninstall a bundle
//	load   <name>               start worker
//	unload <name>               stop worker
//	describe <name>             show input/output schema
//	run <name> [json-body]      run inference (reads stdin if no arg)
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
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

func installCmd(c *client, args []string) {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	name := fs.String("name", "", "override bundle name")
	ref := fs.String("ref", "", "override git ref")
	dir := fs.String("dir", "", "override bundle subdir")
	fs.Parse(args) //nolint:errcheck

	if fs.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "install requires a source URL")
		os.Exit(1)
	}
	body := map[string]string{
		"source": fs.Arg(0),
		"name":   *name,
		"ref":    *ref,
		"dir":    *dir,
	}
	c.post("/v1/models:install", body)
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
  list                          list installed bundles
  get <name>                    show bundle metadata
  rm  <name>                    uninstall bundle
  load   <name>                 start worker process
  unload <name>                 stop worker process
  describe <name>               show input/output schema
  run <name> [json]             run inference (reads stdin if no json arg)

Environment:
  GONNX_HOST   daemon address (default http://localhost:7860)`)
}
