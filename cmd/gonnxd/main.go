// gonnxd is the gonnx model-serving daemon.
//
// Usage:
//
//	gonnxd [--addr :7860] [--state-dir ~/.local/share/gonnx]
package main

import (
	"context"
	"flag"
	"log/slog"
	"net/http"
	"os"
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
	addr := flag.String("addr", ":7860", "TCP address to listen on")
	stateDir := flag.String("state-dir", defaultStateDir(), "gonnx state directory")
	logLevel := flag.String("log-level", "info", "log level: debug|info|warn|error")
	flag.Parse()

	setupLogger(*logLevel)

	slog.Info("starting gonnxd", "addr", *addr, "stateDir", *stateDir)

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
	})

	srv := &http.Server{
		Addr:         *addr,
		Handler:      router,
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Graceful shutdown on SIGINT / SIGTERM.
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
}
