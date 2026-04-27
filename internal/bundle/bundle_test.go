package bundle_test

import (
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

func TestLoad_MissingModelFile(t *testing.T) {
	dir := makeBundle(t, func(dir string) {
		os.Remove(filepath.Join(dir, "model.onnx"))
	})
	_, err := bundle.Load(dir)
	if err == nil {
		t.Fatal("expected error for missing model file")
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
