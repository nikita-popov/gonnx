// Package assets fetches, verifies, and places asset files declared in a
// bundle manifest.
//
// Workflow:
//
//	plan, err := assets.Plan(manifest, bundleDir)
//	if err != nil { ... }
//	for _, item := range plan {
//	    log.Printf("fetching %s (%d bytes)", item.Asset.ID, item.Asset.Size)
//	}
//	if err := assets.Fetch(ctx, plan, nil); err != nil { ... }
//
// A Plan contains only the assets that are absent or whose sha256 does not
// match the on-disk file. Assets that are already correct are excluded.
package assets

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/nikita-popov/gonnx/internal/bundle"
)

// Item is one entry in a fetch plan: an asset that needs to be downloaded.
type Item struct {
	Asset     bundle.Asset
	DestAbs   string // absolute resolved destination path
	CacheMiss string // "absent" | "sha256_mismatch"
}

// Plan inspects bundleDir and returns the list of assets that need fetching.
func Plan(m *bundle.Manifest, bundleDir string) ([]Item, error) {
	var items []Item
	seen := make(map[string]struct{}, len(m.Assets))

	for _, a := range m.Assets {
		if err := validateAsset(a, bundleDir); err != nil {
			return nil, fmt.Errorf("asset %q: %w", a.ID, err)
		}
		if _, dup := seen[a.ID]; dup {
			return nil, fmt.Errorf("asset id %q is not unique", a.ID)
		}
		seen[a.ID] = struct{}{}

		// Normalise sha256 to exactly 64 lower-case hex chars (left-pad with
		// zeros). Tools like sha256sum omit leading zeros, so e.g. a hash
		// starting with "0" may appear as 63 characters in the manifest.
		a.SHA256 = fmt.Sprintf("%064s", strings.ToLower(a.SHA256))

		destAbs := filepath.Join(bundleDir, a.Dest)
		miss, err := cacheMiss(destAbs, a.SHA256)
		if err != nil {
			return nil, fmt.Errorf("asset %q: %w", a.ID, err)
		}
		if miss != "" {
			items = append(items, Item{Asset: a, DestAbs: destAbs, CacheMiss: miss})
		}
	}
	return items, nil
}

// FetchOptions configures the fetch phase.
type FetchOptions struct {
	// HTTPClient overrides the default http.Client. Nil uses http.DefaultClient.
	HTTPClient *http.Client

	// Environ provides environment variable lookups for auth tokens.
	// Nil falls back to os.Getenv.
	Environ func(string) string

	// Progress is called periodically during each asset download.
	// written is the number of bytes written so far; total is the
	// Content-Length (-1 if unknown). Called once per chunk and once
	// more with written==total when the download completes.
	Progress func(id string, written, total int64)
}

// Fetch downloads and verifies all items in the plan.
// Each file is written to a temporary path first, sha256-verified, then
// renamed to its final destination atomically.
func Fetch(ctx context.Context, plan []Item, opts *FetchOptions) error {
	if opts == nil {
		opts = &FetchOptions{}
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = http.DefaultClient
	}
	if opts.Environ == nil {
		opts.Environ = os.Getenv
	}

	for _, item := range plan {
		if err := fetchOne(ctx, item, opts); err != nil {
			return fmt.Errorf("asset %q: %w", item.Asset.ID, err)
		}
	}
	return nil
}

// CheckPresent returns an error listing every asset whose dest file is absent
// or whose sha256 does not match.
func CheckPresent(m *bundle.Manifest, bundleDir string) error {
	var missing []string
	for _, a := range m.Assets {
		destAbs := filepath.Join(bundleDir, a.Dest)
		// Normalise before comparison (same logic as Plan).
		norm := fmt.Sprintf("%064s", strings.ToLower(a.SHA256))
		miss, err := cacheMiss(destAbs, norm)
		if err != nil {
			missing = append(missing, fmt.Sprintf("%s (%v)", a.ID, err))
			continue
		}
		if miss != "" {
			missing = append(missing, fmt.Sprintf("%s (%s)", a.ID, miss))
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf(
			"assets not ready: %s\n\nrun: gonnxctl pull %s",
			strings.Join(missing, ", "),
			"<bundle-name>",
		)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// progressWriter wraps an io.Writer and calls a progress callback after each
// Write, reporting cumulative bytes written and the total size.
type progressWriter struct {
	w       io.Writer
	h       io.Writer // sha256 tee destination
	id      string
	total   int64
	written int64
	cb      func(id string, written, total int64)
}

func (pw *progressWriter) Write(p []byte) (int, error) {
	n, err := pw.w.Write(p)
	pw.written += int64(n)
	if pw.cb != nil {
		pw.cb(pw.id, pw.written, pw.total)
	}
	return n, err
}

func validateAsset(a bundle.Asset, bundleDir string) error {
	if a.ID == "" {
		return errors.New("id is required")
	}
	if a.URL == "" {
		return errors.New("url is required")
	}
	if l := len(a.SHA256); l == 0 || l > 64 {
		return fmt.Errorf("sha256 must be 1-64 hex characters, got %d", l)
	}
	if _, err := hex.DecodeString(fmt.Sprintf("%064s", strings.ToLower(a.SHA256))); err != nil {
		return fmt.Errorf("sha256 is not valid hex: %w", err)
	}
	if a.Dest == "" {
		return errors.New("dest is required")
	}
	destAbs := filepath.Join(bundleDir, a.Dest)
	if !strings.HasPrefix(destAbs, filepath.Clean(bundleDir)+string(os.PathSeparator)) {
		return fmt.Errorf("dest %q escapes bundle directory", a.Dest)
	}
	return nil
}

func cacheMiss(destAbs, expected string) (string, error) {
	f, err := os.Open(destAbs)
	if errors.Is(err, os.ErrNotExist) {
		return "absent", nil
	}
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, expected) {
		return "sha256_mismatch", nil
	}
	return "", nil
}

func fetchOne(ctx context.Context, item Item, opts *FetchOptions) error {
	if err := os.MkdirAll(filepath.Dir(item.DestAbs), 0o755); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, item.Asset.URL, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	if item.Asset.Auth != nil && item.Asset.Auth.Env != "" {
		tok := opts.Environ(item.Asset.Auth.Env)
		if tok == "" {
			return fmt.Errorf("auth env var %q is not set", item.Asset.Auth.Env)
		}
		req.Header.Set("Authorization", "Bearer "+tok)
	}

	resp, err := opts.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("server returned %s", resp.Status)
	}

	// Content-Length may be -1 (unknown) for chunked transfers.
	total := resp.ContentLength

	tmp, err := os.CreateTemp(filepath.Dir(item.DestAbs), ".gonnx-asset-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(tmpName)
	}()

	if item.Asset.Unpack != nil {
		return fmt.Errorf("unpack is not yet implemented")
	}

	h := sha256.New()
	// teeWriter fans out to both the temp file and the sha256 hash.
	tee := io.MultiWriter(tmp, h)

	pw := &progressWriter{
		w:     tee,
		id:    item.Asset.ID,
		total: total,
		cb:    opts.Progress,
	}

	if _, err := io.Copy(pw, resp.Body); err != nil {
		return fmt.Errorf("write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}

	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, item.Asset.SHA256) {
		return fmt.Errorf("sha256 mismatch: expected %s, got %s", item.Asset.SHA256, got)
	}

	if err := os.Rename(tmpName, item.DestAbs); err != nil {
		return err
	}
	return nil
}
