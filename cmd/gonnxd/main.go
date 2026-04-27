// gonnxd is the gonnx model-serving daemon.
//
// Usage:
//
//	gonnxd [--addr :7860] [--state-dir /var/lib/gonnx]
package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/nikita-popov/gonnx/internal/api"
	"github.com/nikita-popov/gonnx/internal/registry"
	"github.com/nikita-popov/gonnx/internal/runtime"
	"github.com/nikita-popov/gonnx/internal/source"
)

func main() {
	addr := flag.String("addr", envOr("GONNXD_ADDR", ":7860"), "TCP address to listen on")
	stateDir := flag.String("state-dir", envOr("GONNXD_STATE_DIR", defaultStateDir()), "gonnx state directory")
	logLevel := flag.String("log-level", envOr("GONNXD_LOG_LEVEL", "info"), "log level: debug|info|warn|error")
	flag.Parse()

	// SDK lives inside the state directory — no extra flag needed.
	// install.sh copies sdk/python there during install/upgrade.
	sdkDir := filepath.Join(*stateDir, "sdk", "python")

	setupLogger(*logLevel)

	slog.Info("starting gonnxd", "addr", *addr, "stateDir", *stateDir, "sdkDir", sdkDir)

	reg, err := registry.Open(filepath.Join(*stateDir, "registry.db"))
	if err != nil {
		slog.Error("open registry", "err", err)
		os.Exit(1)
	}
	defer reg.Close()

	inst := source.New(*stateDir)

	mgr := runtime.NewManager(runtime.Config{
		StateDir:              *stateDir,
		DefaultStartupTimeout: 30 * time.Second,
		DefaultPredictTimeout: 30 * time.Second,
	})

	router := api.NewRouter(api.Services{
		Registry:  reg,
		Installer: inst,
		Manager:   mgr,
		SDKDir:    sdkDir,
	})

	srv := &http.Server{
		Addr:         *addr,
		Handler:      router,
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 10 * time.Minute,
		IdleTimeout:  120 * time.Second,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "err", err)
			os.Exit(1)
		}
	}()
	slog.Info("gonnxd ready", "addr", *addr)

	<-stop
	slog.Info("shutting down")

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	mgr.UnloadAll(ctx)
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("shutdown error", "err", err)
	}
}

// envOr returns the value of the environment variable key, or fallback if unset/empty.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// defaultStateDir returns a sensible per-user state directory.
func defaultStateDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ".gonnx"
	}
	return filepath.Join(home, ".local", "share", "gonnx")
}

func setupLogger(level string) {
	var l slog.Level
	switch level {
	case "debug":
		l = slog.LevelDebug
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: l})))
	_ = exec.Command // keep import used
}
