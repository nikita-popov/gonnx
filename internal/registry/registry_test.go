package registry_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/nikita-popov/gonnx/internal/registry"
)

func openTestRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	path := filepath.Join(t.TempDir(), "registry.db")
	r, err := registry.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

func sampleEntry(name string) *registry.Entry {
	return &registry.Entry{
		Name:        name,
		SourceURL:   "https://github.com/example/repo.git",
		SourceRef:   "master",
		SourceDir:   "models/" + name,
		CommitSHA:   "aabbccdd" + name[:1] + "0000000000000000000000000000000000",
		BundleDir:   "/var/gonnx/bundles/" + name,
		Digest:      "sha256:abc123",
		InstalledAt: time.Now().UTC().Truncate(time.Second),
	}
}

func TestUpsertAndGet(t *testing.T) {
	ctx := context.Background()
	r := openTestRegistry(t)

	e := sampleEntry("resnet")
	if err := r.Upsert(ctx, e); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err := r.Get(ctx, "resnet")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.CommitSHA != e.CommitSHA {
		t.Errorf("CommitSHA = %q, want %q", got.CommitSHA, e.CommitSHA)
	}
	if got.Digest != e.Digest {
		t.Errorf("Digest = %q, want %q", got.Digest, e.Digest)
	}
}

func TestUpsert_UpdatesExisting(t *testing.T) {
	ctx := context.Background()
	r := openTestRegistry(t)

	e := sampleEntry("bert")
	if err := r.Upsert(ctx, e); err != nil {
		t.Fatal(err)
	}

	e.CommitSHA = "deadbeef" + "0000000000000000000000000000000000"
	e.BundleDir = "/new/path"
	if err := r.Upsert(ctx, e); err != nil {
		t.Fatal(err)
	}

	got, _ := r.Get(ctx, "bert")
	if got.CommitSHA != e.CommitSHA {
		t.Errorf("CommitSHA not updated: %q", got.CommitSHA)
	}
	if got.BundleDir != "/new/path" {
		t.Errorf("BundleDir not updated: %q", got.BundleDir)
	}
}

func TestGet_NotFound(t *testing.T) {
	ctx := context.Background()
	r := openTestRegistry(t)

	_, err := r.Get(ctx, "no-such-model")
	if !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

func TestList(t *testing.T) {
	ctx := context.Background()
	r := openTestRegistry(t)

	for _, name := range []string{"c-model", "a-model", "b-model"} {
		if err := r.Upsert(ctx, sampleEntry(name)); err != nil {
			t.Fatal(err)
		}
	}

	entries, err := r.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(entries))
	}
	// Must be sorted by name.
	if entries[0].Name != "a-model" || entries[1].Name != "b-model" || entries[2].Name != "c-model" {
		t.Errorf("unexpected order: %v", []string{entries[0].Name, entries[1].Name, entries[2].Name})
	}
}

func TestDelete(t *testing.T) {
	ctx := context.Background()
	r := openTestRegistry(t)

	e := sampleEntry("whisper")
	if err := r.Upsert(ctx, e); err != nil {
		t.Fatal(err)
	}
	if err := r.Delete(ctx, "whisper"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err := r.Get(ctx, "whisper")
	if !errors.Is(err, registry.ErrNotFound) {
		t.Errorf("expected ErrNotFound after delete, got %v", err)
	}
}

func TestDelete_NonExistent(t *testing.T) {
	ctx := context.Background()
	r := openTestRegistry(t)
	// Should not return an error.
	if err := r.Delete(ctx, "ghost"); err != nil {
		t.Errorf("unexpected error deleting non-existent entry: %v", err)
	}
}
