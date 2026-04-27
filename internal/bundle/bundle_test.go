package bundle_test

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/nikita-popov/gonnx/internal/bundle"
)

// makeBundle creates a minimal valid bundle directory for testing.
func makeBundle(t *testing.T, extra func(dir string)) string {
	t.Helper()
	dir := t.TempDir()

	manifest := `apiVersion: onnxd/v1alpha1
kind: Model
name: test-model
version: 0.1.0
runtime:
  engine: onnxruntime
  model: ./model.onnx
  providers:
    - CPUExecutionProvider
handler:
  type: python
  entrypoint: ./handler.py
  callable: app
interface:
  inputSchema:
    $schema: https://json-schema.org/draft/2020-12/schema
    type: object
    properties:
      x: { type: number }
    required: [x]
  outputSchema:
    type: object
    properties:
      y: { type: number }
policy:
  startupTimeoutMs: 5000
  predictTimeoutMs: 10000
  maxConcurrency: 1
  idleUnloadSeconds: 300
  network: disabled
`
	write(t, dir, "manifest.yaml", manifest)
	write(t, dir, "model.onnx", "placeholder")
	write(t, dir, "handler.py", "app = None\n")

	if extra != nil {
		extra(dir)
	}
	return dir
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoad_Valid(t *testing.T) {
	dir := makeBundle(t, nil)
	b, err := bundle.Load(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if b.Manifest.Name != "test-model" {
		t.Errorf("name = %q, want \"test-model\"", b.Manifest.Name)
	}
	if b.Digest == "" {
		t.Error("digest should not be empty")
	}
}

func TestLoad_MissingManifest(t *testing.T) {
	dir := t.TempDir()
	_, err := bundle.Load(dir)
	if err == nil {
		t.Fatal("expected error for missing manifest")
	}
}

// TestLoad_MissingModelFile verifies that Load succeeds even when model.onnx
// is absent: asset files are downloaded lazily by `gonnxctl pull` and are
// NOT required by Load. Use CheckAssets to gate worker startup instead.
func TestLoad_MissingModelFile(t *testing.T) {
	dir := makeBundle(t, func(dir string) {
		os.Remove(filepath.Join(dir, "model.onnx"))
	})
	_, err := bundle.Load(dir)
	if err != nil {
		t.Fatalf("Load should succeed without asset files, got: %v", err)
	}
}

// TestCheckAssets_MissingModelFile verifies that CheckAssets returns a
// MissingAssetsError when a declared asset dest is absent.
func TestCheckAssets_MissingModelFile(t *testing.T) {
	dir := makeBundle(t, func(dir string) {
		os.Remove(filepath.Join(dir, "model.onnx"))
	})
	b, err := bundle.Load(dir)
	if err != nil {
		t.Fatalf("unexpected Load error: %v", err)
	}
	err = bundle.CheckAssets(dir, b.Manifest)
	if err == nil {
		t.Fatal("expected MissingAssetsError, got nil")
	}
	var mae *bundle.MissingAssetsError
	if !errors.As(err, &mae) {
		t.Fatalf("expected *MissingAssetsError, got %T: %v", err, err)
	}
	if len(mae.Missing) == 0 {
		t.Error("MissingAssetsError.Missing should not be empty")
	}
}

// TestCheckAssets_OK verifies that CheckAssets returns nil when all
// declared asset dest files exist on disk.
func TestCheckAssets_OK(t *testing.T) {
	dir := makeBundle(t, nil) // model.onnx written by makeBundle
	b, err := bundle.Load(dir)
	if err != nil {
		t.Fatalf("unexpected Load error: %v", err)
	}
	if err := bundle.CheckAssets(dir, b.Manifest); err != nil {
		t.Fatalf("unexpected CheckAssets error: %v", err)
	}
}

func TestLoad_MissingHandler(t *testing.T) {
	dir := makeBundle(t, func(dir string) {
		os.Remove(filepath.Join(dir, "handler.py"))
	})
	_, err := bundle.Load(dir)
	if err == nil {
		t.Fatal("expected error for missing handler")
	}
}

func TestLoad_InvalidAPIVersion(t *testing.T) {
	dir := t.TempDir()
	manifest := `apiVersion: onnxd/v99
kind: Model
name: x
version: 1.0.0
runtime:
  engine: onnxruntime
  model: ./model.onnx
handler:
  type: python
  entrypoint: ./handler.py
  callable: app
interface:
  inputSchema: {type: object}
  outputSchema: {type: object}
policy:
  startupTimeoutMs: 1000
  predictTimeoutMs: 1000
`
	write(t, dir, "manifest.yaml", manifest)
	write(t, dir, "model.onnx", "")
	write(t, dir, "handler.py", "")
	_, err := bundle.Load(dir)
	if err == nil {
		t.Fatal("expected error for bad apiVersion")
	}
}

func TestDigest_Deterministic(t *testing.T) {
	dir := makeBundle(t, nil)
	b1, err := bundle.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	b2, err := bundle.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if b1.Digest != b2.Digest {
		t.Errorf("digest not deterministic: %q != %q", b1.Digest, b2.Digest)
	}
}

func TestDigest_ChangesOnContentChange(t *testing.T) {
	dir := makeBundle(t, nil)
	b1, _ := bundle.Load(dir)

	write(t, dir, "model.onnx", "changed")
	b2, _ := bundle.Load(dir)

	if b1.Digest == b2.Digest {
		t.Error("digest should change when file content changes")
	}
}
