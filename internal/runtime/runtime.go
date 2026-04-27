// Package runtime supervises worker processes and proxies requests
// to them over HTTP-on-Unix-domain-socket.
// Each loaded model gets exactly one worker process in v0.
package runtime
