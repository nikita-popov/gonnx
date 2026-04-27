package assets_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/nikita-popov/gonnx/internal/assets"
	"github.com/nikita-popov/gonnx/internal/bundle"
)

func hashOf(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func makeManifest(id, url, dest, hash string) *bundle.Manifest {
	return &bundle.Manifest{
		Assets: []bundle.Asset{
			{ID: id, URL: url, SHA256: hash, Dest: dest},
		},
	}
}

// TestPlan_NothingToDo verifies that Plan returns an empty slice when the
// on-disk file already has the correct sha256.
func TestPlan_NothingToDo(t *testing.T) {
	dir := t.TempDir()
	data := []byte("hello asset")
	hash := hashOf(data)

	dest := "model.onnx"
	if err := os.WriteFile(filepath.Join(dir, dest), data, 0o644); err != nil {
		t.Fatal(err)
	}

	plan, err := assets.Plan(makeManifest("model", "https://example.com", dest, hash), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 0 {
		t.Fatalf("expected empty plan, got %d items", len(plan))
	}
}

// TestPlan_AbsentFile verifies that Plan includes an absent file.
func TestPlan_AbsentFile(t *testing.T) {
	dir := t.TempDir()
	hash := hashOf([]byte("data"))

	plan, err := assets.Plan(makeManifest("model", "https://example.com", "model.onnx", hash), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 1 {
		t.Fatalf("expected 1 item, got %d", len(plan))
	}
	if plan[0].CacheMiss != "absent" {
		t.Errorf("expected cache miss 'absent', got %q", plan[0].CacheMiss)
	}
}

// TestPlan_HashMismatch verifies that Plan includes a file with wrong sha256.
func TestPlan_HashMismatch(t *testing.T) {
	dir := t.TempDir()
	dest := "model.onnx"
	if err := os.WriteFile(filepath.Join(dir, dest), []byte("old content"), 0o644); err != nil {
		t.Fatal(err)
	}
	hash := hashOf([]byte("new content")) // different

	plan, err := assets.Plan(makeManifest("model", "https://example.com", dest, hash), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 1 {
		t.Fatalf("expected 1 item, got %d", len(plan))
	}
	if plan[0].CacheMiss != "sha256_mismatch" {
		t.Errorf("expected 'sha256_mismatch', got %q", plan[0].CacheMiss)
	}
}

// TestPlan_TraversalRejected verifies that dest paths escaping bundleDir are rejected.
func TestPlan_TraversalRejected(t *testing.T) {
	dir := t.TempDir()
	hash := hashOf([]byte("x"))
	m := makeManifest("bad", "https://example.com", "../escape.txt", hash)

	_, err := assets.Plan(m, dir)
	if err == nil {
		t.Fatal("expected error for traversal dest, got nil")
	}
}

// TestFetch_Download verifies a successful download with sha256 verification.
func TestFetch_Download(t *testing.T) {
	data := []byte("model weights")
	hash := hashOf(data)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(data)
	}))
	defer srv.Close()

	dir := t.TempDir()
	m := makeManifest("model", srv.URL+"/model.onnx", "model.onnx", hash)

	plan, err := assets.Plan(m, dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 1 {
		t.Fatalf("expected 1 item in plan, got %d", len(plan))
	}

	var got int64
	opts := &assets.FetchOptions{
		Progress: func(_ string, n int64) { got = n },
	}
	if err := assets.Fetch(context.Background(), plan, opts); err != nil {
		t.Fatal(err)
	}

	bytes, err := os.ReadFile(filepath.Join(dir, "model.onnx"))
	if err != nil {
		t.Fatal(err)
	}
	if string(bytes) != string(data) {
		t.Errorf("file content mismatch")
	}
	if got != int64(len(data)) {
		t.Errorf("progress bytes = %d, want %d", got, len(data))
	}
}

// TestFetch_HashMismatch verifies that a wrong sha256 causes an error and no
// partial file is left at the destination.
func TestFetch_HashMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("corrupted"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	hash := hashOf([]byte("correct content")) // intentionally wrong
	m := makeManifest("model", srv.URL+"/model.onnx", "model.onnx", hash)

	plan, _ := assets.Plan(m, dir)
	err := assets.Fetch(context.Background(), plan, nil)
	if err == nil {
		t.Fatal("expected sha256 mismatch error")
	}
	// Destination must not exist after a failed fetch.
	if _, statErr := os.Stat(filepath.Join(dir, "model.onnx")); !os.IsNotExist(statErr) {
		t.Error("partial file must not exist after sha256 mismatch")
	}
}

// TestFetch_AuthToken verifies that the Bearer token is sent from the env var.
func TestFetch_AuthToken(t *testing.T) {
	data := []byte("private weights")
	hash := hashOf(data)
	const tokenValue = "secret-token"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+tokenValue {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write(data)
	}))
	defer srv.Close()

	dir := t.TempDir()
	m := &bundle.Manifest{
		Assets: []bundle.Asset{
			{
				ID:     "model",
				URL:    srv.URL + "/model.onnx",
				SHA256: hash,
				Dest:   "model.onnx",
				Auth:   &bundle.AssetAuth{Env: "MY_TOKEN"},
			},
		},
	}

	plan, err := assets.Plan(m, dir)
	if err != nil {
		t.Fatal(err)
	}

	opts := &assets.FetchOptions{
		Environ: func(key string) string {
			if key == "MY_TOKEN" {
				return tokenValue
			}
			return ""
		},
	}
	if err := assets.Fetch(context.Background(), plan, opts); err != nil {
		t.Fatal(err)
	}
}

// TestCheckPresent_MissingAssets verifies error message contains asset IDs.
func TestCheckPresent_MissingAssets(t *testing.T) {
	dir := t.TempDir()
	hash := hashOf([]byte("x"))
	m := makeManifest("model", "https://example.com", "model.onnx", hash)

	err := assets.CheckPresent(m, dir)
	if err == nil {
		t.Fatal("expected error for missing asset")
	}
	if !contains(err.Error(), "model") {
		t.Errorf("error should mention asset ID 'model': %v", err)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
