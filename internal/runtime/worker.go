// Package runtime manages model worker processes.
//
// Each worker is an independent OS process that:
//   - owns one ONNX model (loaded once at startup)
//   - listens on a Unix domain socket for JSON requests
//   - implements the HTTP sub-protocol: /health, /describe, /predict, /shutdown
//
// The supervisor (Manager) starts, health-checks, and stops workers.
// Communication with the worker uses a private http.Transport dialled
// over the Unix socket — no TCP ports are allocated.
package runtime

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// WorkerState represents the lifecycle state of a worker process.
type WorkerState int

const (
	StateStarting WorkerState = iota
	StateReady
	StateStopped
	StateFailed
)

func (s WorkerState) String() string {
	switch s {
	case StateStarting:
		return "starting"
	case StateReady:
		return "ready"
	case StateStopped:
		return "stopped"
	case StateFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// Worker represents a running model worker process.
type Worker struct {
	// BundleName is the canonical model name from the manifest.
	BundleName string
	// SocketPath is the Unix domain socket the worker listens on.
	SocketPath string

	cmd    *exec.Cmd
	client *http.Client
	state  WorkerState
}

// State returns the current lifecycle state.
func (w *Worker) State() WorkerState { return w.state }

// newWorker creates (but does not start) a Worker.
func newWorker(name, socketPath string, cmd *exec.Cmd) *Worker {
	transport := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
		},
		MaxIdleConns:    1,
		IdleConnTimeout: 30 * time.Second,
	}
	return &Worker{
		BundleName: name,
		SocketPath: socketPath,
		cmd:        cmd,
		client:     &http.Client{Transport: transport},
		state:      StateStarting,
	}
}

// workerURL builds a URL for the worker's virtual HTTP server.
// The host field is ignored by the custom transport (dialled over Unix socket).
func workerURL(path string) string {
	return "http://worker" + path
}

// socketDir returns the directory used to store worker sockets.
func socketDir(stateDir string) string {
	return filepath.Join(stateDir, "sockets")
}

// socketPath returns the path for a named worker's Unix socket.
func socketPath(stateDir, name string) string {
	return filepath.Join(socketDir(stateDir), name+".sock")
}

// ensureSocketDir creates the sockets directory if needed.
func ensureSocketDir(stateDir string) error {
	return os.MkdirAll(socketDir(stateDir), 0o700)
}
