// Package bundle reads and validates model bundle directories.
//
// A bundle is a directory that contains at minimum:
//   - manifest.yaml
//   - the handler entrypoint declared in manifest.handler.entrypoint
//
// Asset files (model.onnx, voices.bin, …) are NOT required at install time;
// they are downloaded separately by `gonnxctl pull` and verified by
// CheckAssets before a worker is started.
//
// Typical layout:
//
//	bundle/
//	  manifest.yaml
//	  handler.py
//	  requirements.txt
//	  model.onnx        ← present only after `gonnxctl pull`
//	  voices.bin        ← present only after `gonnxctl pull`
//	  examples/
package bundle

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"
)

const manifestFile = "manifest.yaml"

// Bundle is a validated, loaded model bundle.
type Bundle struct {
	// Dir is the absolute path to the bundle directory.
	Dir string
	// Manifest is the parsed manifest.
	Manifest *Manifest
	// Digest is a hex-encoded sha256 over present bundle files.
	// Files declared in assets[] but not yet downloaded are excluded.
	Digest string
}

// Load reads, parses, and verifies a bundle at dir.
//
// It checks:
//   - manifest.yaml exists and is valid
//   - handler entrypoint exists (always committed to git)
//
// It does NOT check asset files (model.onnx etc.). Call CheckAssets
// before starting a worker to ensure all assets are present.
func Load(dir string) (*Bundle, error) {
	dir = filepath.Clean(dir)

	m, err := parseManifest(filepath.Join(dir, manifestFile))
	if err != nil {
		return nil, fmt.Errorf("bundle load: bundle %s: %w", dir, err)
	}

	if err := validateManifest(m); err != nil {
		return nil, fmt.Errorf("bundle load: bundle %s: manifest invalid: %w", dir, err)
	}

	if err := checkCodeFiles(dir, m); err != nil {
		return nil, fmt.Errorf("bundle load: bundle %s: %w", dir, err)
	}

	digest, err := computeDigest(dir)
	if err != nil {
		return nil, fmt.Errorf("bundle load: bundle %s: digest: %w", dir, err)
	}

	return &Bundle{
		Dir:      dir,
		Manifest: m,
		Digest:   digest,
	}, nil
}

// CheckAssets verifies that every asset declared in the manifest has been
// downloaded to its dest path inside dir.
//
// Returns a structured error listing all missing assets so the caller can
// surface a useful message: "run gonnxctl pull <name> first".
func CheckAssets(dir string, m *Manifest) error {
	var missing []string
	for _, a := range m.Assets {
		if a.Dest == "" {
			continue
		}
		dest := filepath.Join(dir, a.Dest)
		if _, err := os.Stat(dest); errors.Is(err, os.ErrNotExist) {
			missing = append(missing, a.Dest)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return &MissingAssetsError{Missing: missing}
}

// MissingAssetsError is returned by CheckAssets when one or more asset
// destination files are absent from the bundle directory.
type MissingAssetsError struct {
	Missing []string
}

func (e *MissingAssetsError) Error() string {
	return fmt.Sprintf(
		"assets not downloaded (%v); run: gonnxctl pull <name>",
		e.Missing,
	)
}

// ModelPath returns the absolute path to the ONNX model file.
func (b *Bundle) ModelPath() string {
	return b.resolve(b.Manifest.Runtime.Model)
}

// HandlerPath returns the absolute path to the handler entrypoint.
func (b *Bundle) HandlerPath() string {
	return b.resolve(b.Manifest.Handler.Entrypoint)
}

func (b *Bundle) resolve(rel string) string {
	if filepath.IsAbs(rel) {
		return rel
	}
	return filepath.Join(b.Dir, rel)
}

// parseManifest reads and unmarshals manifest.yaml.
func parseManifest(path string) (*Manifest, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("manifest.yaml not found")
		}
		return nil, err
	}
	defer f.Close()

	var m Manifest
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	return &m, nil
}

// validateManifest checks required fields.
func validateManifest(m *Manifest) error {
	var errs []error

	if m.APIVersion != APIVersion {
		errs = append(errs, fmt.Errorf("unsupported apiVersion %q (want %q)", m.APIVersion, APIVersion))
	}
	if m.Kind != "Model" {
		errs = append(errs, fmt.Errorf("unsupported kind %q (want \"Model\")", m.Kind))
	}
	if m.Name == "" {
		errs = append(errs, errors.New("name is required"))
	}
	if m.Version == "" {
		errs = append(errs, errors.New("version is required"))
	}
	if m.Runtime.Engine == "" {
		errs = append(errs, errors.New("runtime.engine is required"))
	}
	if m.Runtime.Model == "" {
		errs = append(errs, errors.New("runtime.model is required"))
	}
	if m.Handler.Type == "" {
		errs = append(errs, errors.New("handler.type is required"))
	}
	if m.Handler.Entrypoint == "" {
		errs = append(errs, errors.New("handler.entrypoint is required"))
	}
	if m.Interface.InputSchema == nil {
		errs = append(errs, errors.New("interface.inputSchema is required"))
	}
	if m.Interface.OutputSchema == nil {
		errs = append(errs, errors.New("interface.outputSchema is required"))
	}
	if m.Policy.StartupTimeoutMs <= 0 {
		errs = append(errs, errors.New("policy.startupTimeoutMs must be > 0"))
	}
	if m.Policy.PredictTimeoutMs <= 0 {
		errs = append(errs, errors.New("policy.predictTimeoutMs must be > 0"))
	}

	return errors.Join(errs...)
}

// checkCodeFiles verifies that the handler entrypoint (always in git) exists.
// Asset files (model.onnx etc.) are intentionally NOT checked here.
func checkCodeFiles(dir string, m *Manifest) error {
	handler := filepath.Join(dir, m.Handler.Entrypoint)
	if _, err := os.Stat(handler); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("handler entrypoint not found: %s", m.Handler.Entrypoint)
		}
		return err
	}
	return nil
}

// computeDigest walks the bundle directory and returns a hex sha256
// computed over the sorted list of relative file paths and their contents.
// Directories, symlinks, and files that cannot be opened are skipped.
func computeDigest(dir string) (string, error) {
	type entry struct{ path string }

	var entries []entry
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		entries = append(entries, entry{path: path})
		return nil
	})
	if err != nil {
		return "", err
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].path < entries[j].path
	})

	h := sha256.New()
	for _, e := range entries {
		rel, err := filepath.Rel(dir, e.path)
		if err != nil {
			return "", err
		}

		f, err := os.Open(e.path)
		if errors.Is(err, os.ErrNotExist) {
			// Asset not yet downloaded — skip, will be included after pull.
			continue
		}
		if err != nil {
			return "", err
		}

		// Write the relative path as part of the digest so renames change it.
		fmt.Fprintf(h, "%s\x00", rel)
		if _, err := io.Copy(h, f); err != nil {
			f.Close()
			return "", err
		}
		f.Close()
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}
