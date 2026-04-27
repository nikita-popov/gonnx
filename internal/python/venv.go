// Package python manages per-bundle Python virtual environments.
//
// Each bundle gets an isolated venv at bundleDir/.venv. Setup is idempotent:
// it re-runs only when requirements.txt or the SDK content changes (tracked
// via a sha256 sentinel file at .venv/gonnx.installed).
package python

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

const (
	// venvDir is the venv directory name inside bundleDir.
	venvDir = ".venv"
	// sentinelFile records the inputs that were used to build the venv.
	// If missing or stale, Setup re-installs.
	sentinelFile = ".venv/gonnx.installed"
)

// SetupOptions configures the venv setup.
type SetupOptions struct {
	// SDKDir is the absolute path to sdk/python (the gonnx Python package).
	// It is installed in editable/wheel mode before requirements.txt.
	SDKDir string

	// Progress is called with status lines during setup.
	// Optional.
	Progress func(msg string)
}

// Setup creates (or reuses) a Python venv in bundleDir/.venv, installs the
// gonnx SDK, then installs the bundle requirements.txt.
//
// It is safe to call concurrently for different bundle directories.
func Setup(ctx context.Context, bundleDir string, opts SetupOptions) error {
	if opts.Progress == nil {
		opts.Progress = func(string) {}
	}

	reqFile := filepath.Join(bundleDir, "requirements.txt")
	reqHash, err := fileHash(reqFile)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("python: hash requirements.txt: %w", err)
	}

	// Hash SDK source files so that upgrading the SDK (without changing its
	// path) also triggers a venv rebuild.
	sdkHash, err := dirHash(opts.SDKDir)
	if err != nil && opts.SDKDir != "" {
		return fmt.Errorf("python: hash sdk: %w", err)
	}

	// Sentinel encodes: sdkDir + sdk content hash + requirements hash.
	sentinelContent := opts.SDKDir + "\n" + sdkHash + "\n" + reqHash
	sentinelPath := filepath.Join(bundleDir, sentinelFile)

	if current, err := os.ReadFile(sentinelPath); err == nil {
		if strings.TrimSpace(string(current)) == strings.TrimSpace(sentinelContent) {
			opts.Progress("venv already up-to-date, skipping")
			return nil
		}
	}

	venv := filepath.Join(bundleDir, venvDir)

	// 1. Create venv (--clear resets a stale one).
	opts.Progress("creating venv")
	if err := run(ctx, bundleDir, opts.Progress, "python3", "-m", "venv", "--clear", venv); err != nil {
		return fmt.Errorf("python: create venv: %w", err)
	}

	pip := filepath.Join(venv, "bin", "pip")

	// 2. Upgrade pip silently.
	if err := run(ctx, bundleDir, opts.Progress, pip, "install", "--quiet", "--upgrade", "pip"); err != nil {
		return fmt.Errorf("python: upgrade pip: %w", err)
	}

	// 3. Install gonnx SDK.
	if opts.SDKDir != "" {
		opts.Progress("installing gonnx SDK")
		if err := run(ctx, bundleDir, opts.Progress, pip, "install", opts.SDKDir); err != nil {
			return fmt.Errorf("python: install gonnx SDK: %w", err)
		}
	}

	// 4. Install bundle requirements.
	if reqHash != "" {
		opts.Progress("installing requirements.txt")
		if err := run(ctx, bundleDir, opts.Progress, pip, "install", "-r", reqFile); err != nil {
			return fmt.Errorf("python: install requirements: %w", err)
		}
	}

	// 5. Write sentinel.
	if err := os.MkdirAll(filepath.Dir(sentinelPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(sentinelPath, []byte(sentinelContent), 0o644); err != nil {
		return fmt.Errorf("python: write sentinel: %w", err)
	}

	opts.Progress("venv ready")
	return nil
}

// VenvPython returns the path to the venv python3 binary if the venv exists,
// otherwise returns "python3" (system fallback).
func VenvPython(bundleDir string) string {
	p := filepath.Join(bundleDir, venvDir, "bin", "python3")
	if _, err := os.Stat(p); err == nil {
		return p
	}
	return "python3"
}

// ---------------------------------------------------------------------------

// run executes a command, capturing combined stdout+stderr.
// On failure the output is included in the returned error so callers
// (and the client) get actionable diagnostics, not just "exit status 1".
// Each output line is also forwarded to progress so the client sees
// real-time pip output during pull streaming.
func run(ctx context.Context, dir string, progress func(string), name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir

	var buf bytes.Buffer
	// Tee: capture for error message AND write to daemon stderr for logs.
	mw := io.MultiWriter(&buf, os.Stderr)
	cmd.Stdout = mw
	cmd.Stderr = mw

	err := cmd.Run()
	if err != nil {
		out := strings.TrimSpace(buf.String())
		if out != "" {
			// Forward the full output to the client via progress stream.
			for _, line := range strings.Split(out, "\n") {
				if l := strings.TrimSpace(line); l != "" {
					progress(l)
				}
			}
			return fmt.Errorf("%w\n%s", err, out)
		}
		return err
	}
	return nil
}

func fileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// dirHash returns a deterministic sha256 over all regular files under dir,
// sorted by relative path. Returns "" if dir is empty string.
func dirHash(dir string) (string, error) {
	if dir == "" {
		return "", nil
	}
	var paths []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.Type().IsRegular() {
			return nil
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)

	h := sha256.New()
	for _, p := range paths {
		rel, _ := filepath.Rel(dir, p)
		fmt.Fprintf(h, "%s\n", rel)
		f, err := os.Open(p)
		if err != nil {
			return "", err
		}
		_, err = io.Copy(h, f)
		f.Close()
		if err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
