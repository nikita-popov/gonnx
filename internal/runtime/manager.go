package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/nikita-popov/gonnx/internal/bundle"
	"github.com/nikita-popov/gonnx/internal/python"
	"github.com/nikita-popov/gonnx/internal/sysdeps"
)

// Config holds configuration for the Manager.
type Config struct {
	// StateDir is the gonnx state directory (e.g. ~/.local/share/gonnx).
	StateDir string
	// DefaultStartupTimeout is the maximum time to wait for /health.
	DefaultStartupTimeout time.Duration
	// DefaultPredictTimeout is applied when the bundle policy is 0.
	DefaultPredictTimeout time.Duration
}

// LoadRequest carries the information needed to start a worker.
type LoadRequest struct {
	// Name is the canonical bundle name.
	Name string
	// BundleDir is the absolute path to the materialized bundle.
	BundleDir string
	// ModelPath is the absolute path to the model file (.onnx, .pt, etc.).
	ModelPath string
	// Engine is the value of runtime.engine from the manifest (e.g. "onnxruntime", "torch").
	// Passed to the worker as GONNX_ENGINE so the SDK can skip onnxruntime for non-ONNX backends.
	Engine string
	// Providers is the ordered list of execution providers.
	Providers []string
	// HandlerEntrypoint is the path to the handler script.
	HandlerEntrypoint string
	// HandlerCallable is the object/function name inside the script.
	HandlerCallable string
	// StartupTimeout overrides Config.DefaultStartupTimeout when > 0.
	StartupTimeout time.Duration
	// Env holds extra environment variables for the worker process.
	Env []string
	// SystemDeps is the list of system dependencies declared in the manifest.
	// Manager checks them before starting the process and sets StateDegraded
	// if any are missing, instead of refusing to load.
	SystemDeps []bundle.SystemDep
}

// Manager supervises worker processes.
type Manager struct {
	cfg     Config
	mu      sync.RWMutex
	workers map[string]*Worker // keyed by bundle name
}

// NewManager creates a Manager with the given configuration.
func NewManager(cfg Config) *Manager {
	if cfg.DefaultStartupTimeout == 0 {
		cfg.DefaultStartupTimeout = 30 * time.Second
	}
	if cfg.DefaultPredictTimeout == 0 {
		cfg.DefaultPredictTimeout = 30 * time.Second
	}
	return &Manager{
		cfg:     cfg,
		workers: make(map[string]*Worker),
	}
}

// Load starts a worker process for the given bundle.
// It blocks until the worker is healthy or the startup timeout expires.
// If a worker for this bundle is already loaded, Load is a no-op.
//
// If system dependencies declared in req.SystemDeps are missing, the worker
// is started normally but marked as StateDegraded. Subsequent Predict calls
// will return an actionable error message instead of forwarding the request.
func (m *Manager) Load(ctx context.Context, req LoadRequest) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if w, ok := m.workers[req.Name]; ok && w.state == StateReady {
		return nil
	}

	// Check system dependencies before starting the process.
	var degradedReasons []string
	if missing := sysdeps.Check(req.SystemDeps); len(missing) > 0 {
		for _, dep := range missing {
			degradedReasons = append(degradedReasons, dep.String())
		}
		slog.Warn("worker has missing system dependencies",
			"worker", req.Name,
			"missing", degradedReasons,
		)
	}

	if err := ensureSocketDir(m.cfg.StateDir); err != nil {
		return fmt.Errorf("runtime: socket dir: %w", err)
	}

	sock := socketPath(m.cfg.StateDir, req.Name)
	// Remove stale socket from a previous run.
	_ = os.Remove(sock)

	cmd := m.buildCmd(req, sock)
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("runtime: start worker %q: %w", req.Name, err)
	}

	// waitCh receives the error from cmd.Wait() as soon as the process exits.
	// This is the only correct way to observe process termination without
	// racing on ProcessState — Wait() reaps the child and populates ProcessState.
	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()

	w := newWorker(req.Name, sock, cmd)
	w.waitCh = waitCh
	m.workers[req.Name] = w

	timeout := req.StartupTimeout
	if timeout == 0 {
		timeout = m.cfg.DefaultStartupTimeout
	}

	if err := m.waitHealthy(ctx, w, timeout); err != nil {
		w.state = StateFailed
		_ = cmd.Process.Kill()
		return fmt.Errorf("runtime: worker %q failed health check: %w", req.Name, err)
	}

	if len(degradedReasons) > 0 {
		w.state = StateDegraded
		w.DegradedReasons = degradedReasons
	} else {
		w.state = StateReady
	}
	return nil
}

// Unload sends a /shutdown request to the worker and waits for it to exit.
func (m *Manager) Unload(ctx context.Context, name string) error {
	m.mu.Lock()
	w, ok := m.workers[name]
	if !ok {
		m.mu.Unlock()
		return nil
	}
	delete(m.workers, name)
	m.mu.Unlock()

	// Best-effort graceful shutdown.
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, workerURL("/shutdown"), nil)
	_, _ = w.client.Do(req)

	select {
	case <-w.waitCh:
	case <-time.After(5 * time.Second):
		_ = w.cmd.Process.Kill()
		<-w.waitCh
	}

	_ = os.Remove(w.SocketPath)
	w.state = StateStopped
	return nil
}

// Predict forwards a JSON predict request to the named worker and returns
// the raw JSON response body.
//
// If the worker is in StateDegraded, Predict returns an actionable error
// listing the missing system dependencies instead of forwarding the request.
func (m *Manager) Predict(ctx context.Context, name string, reqBody []byte) ([]byte, error) {
	m.mu.RLock()
	w, ok := m.workers[name]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("runtime: worker %q not loaded", name)
	}
	switch w.state {
	case StateDegraded:
		return nil, fmt.Errorf("runtime: %s", degradedMsg(w.DegradedReasons))
	case StateReady:
		// ok
	default:
		return nil, fmt.Errorf("runtime: worker %q is %s", name, w.state)
	}

	httpReq, err := http.NewRequestWithContext(ctx,
		http.MethodPost, workerURL("/predict"),
		bytes.NewReader(reqBody),
	)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := w.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("runtime: predict %q: %w", name, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("runtime: read predict response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("runtime: worker %q returned HTTP %d: %s", name, resp.StatusCode, body)
	}

	return body, nil
}

// Workers returns a snapshot of all registered workers and their states.
func (m *Manager) Workers() map[string]WorkerState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]WorkerState, len(m.workers))
	for k, v := range m.workers {
		out[k] = v.state
	}
	return out
}

// UnloadAll gracefully stops all workers. Intended for daemon shutdown.
func (m *Manager) UnloadAll(ctx context.Context) {
	m.mu.RLock()
	names := make([]string, 0, len(m.workers))
	for k := range m.workers {
		names = append(names, k)
	}
	m.mu.RUnlock()

	for _, name := range names {
		_ = m.Unload(ctx, name)
	}
}

// waitHealthy polls the worker's /health endpoint until it responds 200,
// the timeout expires, or the process exits (via waitCh).
func (m *Manager) waitHealthy(ctx context.Context, w *Worker, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return errors.New("startup timeout exceeded")
		case waitErr := <-w.waitCh:
			// Process exited before becoming healthy — report exit status.
			if waitErr != nil {
				return fmt.Errorf("worker exited: %w", waitErr)
			}
			return errors.New("worker exited unexpectedly")
		case <-ticker.C:
			if err := m.checkHealth(ctx, w); err == nil {
				return nil
			}
		}
	}
}

// checkHealth performs a single GET /health request.
func (m *Manager) checkHealth(ctx context.Context, w *Worker) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, workerURL("/health"), nil)
	if err != nil {
		return err
	}
	resp, err := w.client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health: HTTP %d", resp.StatusCode)
	}
	return nil
}

// buildCmd constructs the worker os/exec.Cmd.
// It uses the bundle's venv python3 if available, otherwise system python3.
func (m *Manager) buildCmd(req LoadRequest, sock string) *exec.Cmd {
	pythonBin := python.VenvPython(req.BundleDir)

	args := []string{"-m", "gonnx.worker",
		"--entrypoint", req.HandlerEntrypoint,
		"--callable", req.HandlerCallable,
	}
	cmd := exec.Command(pythonBin, args...)
	cmd.Dir = req.BundleDir

	engine := req.Engine
	if engine == "" {
		engine = "onnxruntime"
	}

	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"GONNX_SOCKET=" + sock,
		"GONNX_MODEL_PATH=" + req.ModelPath,
		"GONNX_BUNDLE_DIR=" + req.BundleDir,
		"GONNX_PROVIDERS=" + joinProviders(req.Providers),
		"GONNX_ENGINE=" + engine,
	}
	env = append(env, req.Env...)
	cmd.Env = env

	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	return cmd
}

// joinProviders concatenates execution provider names with a comma.
func joinProviders(pp []string) string {
	if len(pp) == 0 {
		return "CPUExecutionProvider"
	}
	out := pp[0]
	for _, p := range pp[1:] {
		out += "," + p
	}
	return out
}

// HealthResponse is the JSON body returned by a healthy worker on GET /health.
type HealthResponse struct {
	Status string `json:"status"` // always "ok"
	Model  string `json:"model"`
}

// DescribeResponse is the JSON body returned by GET /describe.
type DescribeResponse struct {
	Name    string         `json:"name"`
	Version string         `json:"version"`
	Inputs  map[string]any `json:"inputs"`
	Outputs map[string]any `json:"outputs"`
}

// Describe fetches the /describe response from a loaded worker.
func (m *Manager) Describe(ctx context.Context, name string) (*DescribeResponse, error) {
	m.mu.RLock()
	w, ok := m.workers[name]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("runtime: worker %q not loaded", name)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, workerURL("/describe"), nil)
	if err != nil {
		return nil, err
	}
	resp, err := w.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var dr DescribeResponse
	if err := json.NewDecoder(resp.Body).Decode(&dr); err != nil {
		return nil, fmt.Errorf("runtime: decode describe: %w", err)
	}
	return &dr, nil
}
