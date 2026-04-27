// Package api implements the gonnxd external HTTP API.
//
// Routes:
//
//	POST   /v1/models:install          install a bundle from Git
//	POST   /v1/models/{name}:pull      download bundle assets (NDJSON stream)
//	GET    /v1/models                  list installed bundles
//	GET    /v1/models/{name}           get bundle metadata
//	DELETE /v1/models/{name}           uninstall a bundle
//	POST   /v1/models/{name}:load      load (start worker)
//	POST   /v1/models/{name}:unload    unload (stop worker)
//	GET    /v1/models/{name}:describe  describe inputs/outputs
//	POST   /v1/models/{name}:predict   run inference
//	GET    /v1/healthz                 daemon liveness probe
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/nikita-popov/gonnx/internal/assets"
	"github.com/nikita-popov/gonnx/internal/bundle"
	"github.com/nikita-popov/gonnx/internal/python"
	"github.com/nikita-popov/gonnx/internal/registry"
	"github.com/nikita-popov/gonnx/internal/runtime"
	"github.com/nikita-popov/gonnx/internal/schema"
	"github.com/nikita-popov/gonnx/internal/source"
)

// Services groups the dependencies injected into the HTTP handlers.
type Services struct {
	Registry  *registry.Registry
	Installer *source.Installer
	Manager   *runtime.Manager
	// SDKDir is the absolute path to sdk/python — installed into every
	// bundle venv during pull. Empty string disables SDK installation.
	SDKDir string
}

// NewRouter builds and returns the HTTP mux for the gonnxd API.
func NewRouter(svc Services) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /v1/healthz", handleHealthz)
	mux.HandleFunc("POST /v1/models:install", handleInstall(svc))
	mux.HandleFunc("GET /v1/models", handleList(svc))

	// Routes with a model name segment + optional :action.
	mux.HandleFunc("GET /v1/models/", handleModel(svc))
	mux.HandleFunc("DELETE /v1/models/", handleModel(svc))
	mux.HandleFunc("POST /v1/models/", handleModel(svc))

	return withLogging(mux)
}

func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		slog.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.status,
		)
	})
}

type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

// --- /v1/healthz -----------------------------------------------------------

func handleHealthz(w http.ResponseWriter, _ *http.Request) {
	jsonOK(w, map[string]string{"status": "ok"})
}

// --- POST /v1/models:install -----------------------------------------------

type installRequest struct {
	Source string `json:"source"`
	Name   string `json:"name"`
	Ref    string `json:"ref"`
	Dir    string `json:"dir"`
}

func handleInstall(svc Services) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req installRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonErr(w, http.StatusBadRequest, "invalid request body: "+err.Error())
			return
		}
		if req.Source == "" {
			jsonErr(w, http.StatusBadRequest, "source is required")
			return
		}

		ref, err := source.ParseRef(req.Source, req.Ref, req.Dir)
		if err != nil {
			jsonErr(w, http.StatusBadRequest, err.Error())
			return
		}

		bundleDir, commitSHA, err := svc.Installer.Install(r.Context(), ref)
		if err != nil {
			jsonErr(w, http.StatusInternalServerError, err.Error())
			return
		}

		b, err := bundle.Load(bundleDir)
		if err != nil {
			jsonErr(w, http.StatusInternalServerError, "bundle load: "+err.Error())
			return
		}

		name := req.Name
		if name == "" {
			name = b.Manifest.Name
		}

		entry := &registry.Entry{
			Name:      name,
			SourceURL: ref.RepoURL,
			SourceRef: ref.Ref,
			SourceDir: ref.Subdir,
			CommitSHA: commitSHA,
			BundleDir: bundleDir,
			Digest:    b.Digest,
		}
		if err := svc.Registry.Upsert(r.Context(), entry); err != nil {
			jsonErr(w, http.StatusInternalServerError, err.Error())
			return
		}

		w.WriteHeader(http.StatusCreated)
		jsonOK(w, entryToResponse(entry))
	}
}

// --- GET /v1/models --------------------------------------------------------

func handleList(svc Services) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		entries, err := svc.Registry.List(r.Context())
		if err != nil {
			jsonErr(w, http.StatusInternalServerError, err.Error())
			return
		}
		states := svc.Manager.Workers()
		type item struct {
			*modelResponse
			State string `json:"state"`
		}
		items := make([]item, 0, len(entries))
		for _, e := range entries {
			st := "unloaded"
			if s, ok := states[e.Name]; ok {
				st = s.String()
			}
			items = append(items, item{entryToResponse(e), st})
		}
		jsonOK(w, map[string]any{"models": items})
	}
}

// --- /v1/models/{name}[:{action}] ------------------------------------------

func handleModel(svc Services) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		path := strings.TrimPrefix(r.URL.Path, "/v1/models/")
		name, action, _ := strings.Cut(path, ":")
		name = strings.TrimSuffix(name, "/")

		if name == "" {
			jsonErr(w, http.StatusBadRequest, "model name is required")
			return
		}

		switch {
		case r.Method == http.MethodGet && action == "":
			handleGetModel(svc, name, w, r)
		case r.Method == http.MethodDelete && action == "":
			handleDeleteModel(svc, name, w, r)
		case r.Method == http.MethodPost && action == "pull":
			handlePull(svc, name, w, r)
		case r.Method == http.MethodPost && action == "load":
			handleLoad(svc, name, w, r)
		case r.Method == http.MethodPost && action == "unload":
			handleUnload(svc, name, w, r)
		case r.Method == http.MethodGet && action == "describe":
			handleDescribe(svc, name, w, r)
		case r.Method == http.MethodPost && action == "predict":
			handlePredict(svc, name, w, r)
		default:
			jsonErr(w, http.StatusNotFound,
				fmt.Sprintf("unknown route %s %s", r.Method, r.URL.Path))
		}
	}
}

func handleGetModel(svc Services, name string, w http.ResponseWriter, r *http.Request) {
	e, err := svc.Registry.Get(r.Context(), name)
	if errors.Is(err, registry.ErrNotFound) {
		jsonErr(w, http.StatusNotFound, "model not found: "+name)
		return
	}
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, entryToResponse(e))
}

// handleDeleteModel unloads the worker (if running), removes the registry
// entry, and deletes the bundle directory from disk.
func handleDeleteModel(svc Services, name string, w http.ResponseWriter, r *http.Request) {
	// 1. Stop the worker (best-effort — ignore if not loaded).
	_ = svc.Manager.Unload(r.Context(), name)

	// 2. Fetch bundleDir before removing the registry entry.
	e, err := svc.Registry.Get(r.Context(), name)
	if err != nil && !errors.Is(err, registry.ErrNotFound) {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 3. Remove registry entry.
	if err := svc.Registry.Delete(r.Context(), name); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 4. Delete bundle directory (assets + venv + worktree).
	if e != nil && e.BundleDir != "" {
		if err := os.RemoveAll(e.BundleDir); err != nil {
			slog.Warn("rm bundleDir", "dir", e.BundleDir, "err", err)
		}
	}

	w.WriteHeader(http.StatusNoContent)
}

// handlePull streams asset download progress as NDJSON to the client.
//
// Each line is a JSON object (one of):
//
//	{"status":"pulling", "asset":"model",   "written":169803776, "total":334118912}
//	{"status":"venv",    "msg":"creating venv"}
//	{"status":"done",    "name":"kokoro-tts"}
//	{"status":"error",   "error":"..."}
//
// The response is always 200 OK; errors are encoded as a final NDJSON line.
func handlePull(svc Services, name string, w http.ResponseWriter, r *http.Request) {
	e, err := svc.Registry.Get(r.Context(), name)
	if errors.Is(err, registry.ErrNotFound) {
		jsonErr(w, http.StatusNotFound, "model not found: "+name)
		return
	}
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	b, err := bundle.Load(e.BundleDir)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "bundle load: "+err.Error())
		return
	}

	plan, err := assets.Plan(b.Manifest, e.BundleDir)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "asset plan: "+err.Error())
		return
	}

	// Switch to NDJSON streaming — headers must be set before any Write.
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	flusher, canFlush := w.(http.Flusher)

	emit := func(v any) {
		line, _ := json.Marshal(v)
		w.Write(append(line, '\n')) //nolint:errcheck
		if canFlush {
			flusher.Flush()
		}
	}

	if len(plan) > 0 {
		progCB := func(id string, written, total int64) {
			emit(map[string]any{
				"status":  "pulling",
				"asset":   id,
				"written": written,
				"total":   total,
			})
		}
		if fetchErr := assets.Fetch(r.Context(), plan, &assets.FetchOptions{
			Progress: progCB,
		}); fetchErr != nil {
			emit(map[string]any{"status": "error", "error": fetchErr.Error()})
			return
		}
	}

	// Set up the per-bundle Python venv.
	venvErr := python.Setup(r.Context(), e.BundleDir, python.SetupOptions{
		SDKDir: svc.SDKDir,
		Progress: func(msg string) {
			emit(map[string]any{"status": "venv", "msg": msg})
		},
	})
	if venvErr != nil {
		emit(map[string]any{"status": "error", "error": venvErr.Error()})
		return
	}

	skipped := len(plan) == 0
	emit(map[string]any{"status": "done", "name": name, "skipped": skipped})
}

func handleLoad(svc Services, name string, w http.ResponseWriter, r *http.Request) {
	e, err := svc.Registry.Get(r.Context(), name)
	if errors.Is(err, registry.ErrNotFound) {
		jsonErr(w, http.StatusNotFound, "model not found: "+name)
		return
	}
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	b, err := bundle.Load(e.BundleDir)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "bundle: "+err.Error())
		return
	}
	m := b.Manifest

	// Verify assets before attempting to start the worker.
	if err := bundle.CheckAssets(e.BundleDir, m); err != nil {
		jsonErr(w, http.StatusConflict, err.Error())
		return
	}

	if err := svc.Manager.Load(r.Context(), runtime.LoadRequest{
		Name:              name,
		BundleDir:         e.BundleDir,
		ModelPath:         b.ModelPath(),
		Providers:         m.Runtime.Providers,
		HandlerEntrypoint: b.HandlerPath(),
		HandlerCallable:   m.Handler.Callable,
		StartupTimeout:    msDuration(m.Policy.StartupTimeoutMs),
	}); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, map[string]string{"status": "loaded", "name": name})
}

func handleUnload(svc Services, name string, w http.ResponseWriter, r *http.Request) {
	if err := svc.Manager.Unload(r.Context(), name); err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonOK(w, map[string]string{"status": "unloaded", "name": name})
}

func handleDescribe(svc Services, name string, w http.ResponseWriter, r *http.Request) {
	dr, err := svc.Manager.Describe(r.Context(), name)
	if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	jsonOK(w, dr)
}

func handlePredict(svc Services, name string, w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	var raw json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		jsonErr(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return
	}

	e, err := svc.Registry.Get(r.Context(), name)
	if errors.Is(err, registry.ErrNotFound) {
		jsonErr(w, http.StatusNotFound, "model not found: "+name)
		return
	}
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, err.Error())
		return
	}

	b, err := bundle.Load(e.BundleDir)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "bundle: "+err.Error())
		return
	}

	v, err := schema.Compile(b.Manifest.Interface.InputSchema)
	if err != nil {
		jsonErr(w, http.StatusInternalServerError, "inputSchema compile: "+err.Error())
		return
	}

	if err := v.Validate(raw); err != nil {
		if schema.IsValidationError(err) {
			jsonErr(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		jsonErr(w, http.StatusBadRequest, err.Error())
		return
	}

	resp, err := svc.Manager.Predict(r.Context(), name, raw)
	if err != nil {
		jsonErr(w, http.StatusBadGateway, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(resp) //nolint:errcheck
}

// --- helpers ---------------------------------------------------------------

type modelResponse struct {
	Name        string `json:"name"`
	SourceURL   string `json:"sourceUrl"`
	SourceRef   string `json:"sourceRef"`
	SourceDir   string `json:"sourceDir"`
	CommitSHA   string `json:"commitSha"`
	Digest      string `json:"digest"`
	BundleDir   string `json:"bundleDir"`
	InstalledAt string `json:"installedAt"`
}

func entryToResponse(e *registry.Entry) *modelResponse {
	return &modelResponse{
		Name:        e.Name,
		SourceURL:   e.SourceURL,
		SourceRef:   e.SourceRef,
		SourceDir:   e.SourceDir,
		CommitSHA:   e.CommitSHA,
		Digest:      e.Digest,
		BundleDir:   e.BundleDir,
		InstalledAt: e.InstalledAt.Format("2006-01-02T15:04:05Z"),
	}
}

func jsonOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}

func jsonErr(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]string{"error": msg}) //nolint:errcheck
}
