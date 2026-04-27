package runtime_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nikita-popov/gonnx/internal/runtime"
)

// startFakeWorker launches a minimal HTTP server over a Unix socket
// to simulate a worker process. It returns the socket path and a
// cancel func that stops the server.
func startFakeWorker(t *testing.T, sock string, predictResponse any) func() {
	t.Helper()

	_ = os.MkdirAll(filepath.Dir(sock), 0o700)
	_ = os.Remove(sock)

	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("fake worker listen: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	mux.HandleFunc("/predict", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(predictResponse)
	})
	mux.HandleFunc("/describe", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(runtime.DescribeResponse{
			Name: "test", Version: "0.1.0",
		})
	})
	mux.HandleFunc("/shutdown", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	srv := &http.Server{Handler: mux}
	go srv.Serve(ln) //nolint:errcheck

	return func() {
		srv.Close()
		ln.Close()
		os.Remove(sock)
	}
}

// TestManager_LoadAndPredict verifies that the Manager can proxy a
// predict request to a fake worker over a Unix socket.
//
// Because we cannot spawn a real Python process in unit tests, we inject
// a pre-started fake worker directly via the exported InjectWorker helper
// (see export_test.go).
func TestManager_LoadAndPredict(t *testing.T) {
	ctx := context.Background()
	stateDir := t.TempDir()

	mgr := runtime.NewManager(runtime.Config{
		StateDir:              stateDir,
		DefaultStartupTimeout: 5 * time.Second,
	})

	sock := filepath.Join(stateDir, "sockets", "mymodel.sock")
	stop := startFakeWorker(t, sock, map[string]any{"label": "cat", "score": 0.97})
	t.Cleanup(stop)

	runtime.InjectWorker(mgr, "mymodel", sock)

	body, err := mgr.Predict(ctx, "mymodel", []byte(`{"x": 1}`))
	if err != nil {
		t.Fatalf("Predict: %v", err)
	}

	var result map[string]any
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result["label"] != "cat" {
		t.Errorf("label = %v, want cat", result["label"])
	}
}

func TestManager_PredictUnknownWorker(t *testing.T) {
	ctx := context.Background()
	mgr := runtime.NewManager(runtime.Config{StateDir: t.TempDir()})

	_, err := mgr.Predict(ctx, "no-such-model", []byte(`{}`))
	if err == nil {
		t.Fatal("expected error for unknown worker")
	}
}

func TestManager_Describe(t *testing.T) {
	ctx := context.Background()
	stateDir := t.TempDir()
	mgr := runtime.NewManager(runtime.Config{StateDir: stateDir})

	sock := filepath.Join(stateDir, "sockets", "descmodel.sock")
	stop := startFakeWorker(t, sock, nil)
	t.Cleanup(stop)

	runtime.InjectWorker(mgr, "descmodel", sock)

	dr, err := mgr.Describe(ctx, "descmodel")
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if dr.Name != "test" {
		t.Errorf("Name = %q, want test", dr.Name)
	}
}

func TestManager_WorkerStates(t *testing.T) {
	stateDir := t.TempDir()
	mgr := runtime.NewManager(runtime.Config{StateDir: stateDir})

	sock := filepath.Join(stateDir, "sockets", "statemodel.sock")
	stop := startFakeWorker(t, sock, nil)
	t.Cleanup(stop)

	runtime.InjectWorker(mgr, "statemodel", sock)

	states := mgr.Workers()
	if states["statemodel"] != runtime.StateReady {
		t.Errorf("state = %v, want Ready", states["statemodel"])
	}
}
