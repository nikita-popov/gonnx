package source

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// copyDir recursively copies src directory into dst.
// dst must already exist.
func copyDir(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)

		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}

		if d.Type()&fs.ModeSymlink != 0 {
			// Skip symlinks — bundles should not contain them.
			return nil
		}

		return copyFile(path, target)
	})
}

// copyFile copies a single regular file.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}
