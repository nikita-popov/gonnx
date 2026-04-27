// Package source handles Git-based bundle installation and updates.
//
// It shells out to the system git binary to perform clone, fetch,
// sparse-checkout, and commit resolution. No Go-native Git library is used
// so that sparse checkout and partial clone are fully supported.
//
// Directory layout managed by this package (under stateDir):
//
//	repos/<origin-hash>/   bare mirror of the remote repository
//	bundles/<name>/<sha>/  materialized bundle for one revision
package source

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Installer installs and updates model bundles from Git sources.
type Installer struct {
	// StateDir is the root directory for repos and bundles caches.
	// Typically ~/.local/share/gonnx.
	StateDir string
}

// New creates an Installer rooted at stateDir.
func New(stateDir string) *Installer {
	return &Installer{StateDir: stateDir}
}

// Install clones (or updates) the remote, materializes the bundle subdir
// at the resolved commit, and returns the local bundle directory and the
// exact commit SHA.
func (inst *Installer) Install(ctx context.Context, ref *Ref) (bundleDir, commitSHA string, err error) {
	mirrorDir := inst.mirrorDir(ref.RepoURL)

	if err := inst.syncMirror(ctx, ref.RepoURL, mirrorDir); err != nil {
		return "", "", err
	}

	sha, err := inst.resolveCommit(ctx, mirrorDir, ref.Ref)
	if err != nil {
		return "", "", err
	}

	dest := inst.bundleDir(ref, sha)
	if _, err := os.Stat(dest); err == nil {
		// Already materialized — idempotent.
		return dest, sha, nil
	}

	if err := inst.materialize(ctx, mirrorDir, ref, sha, dest); err != nil {
		_ = os.RemoveAll(dest)
		return "", "", err
	}

	return dest, sha, nil
}

// Update fetches the latest changes for ref and materializes a new revision
// if the commit has advanced. Returns the new bundle directory and commit SHA.
// If the commit is unchanged it returns the existing directory.
func (inst *Installer) Update(ctx context.Context, ref *Ref, currentSHA string) (bundleDir, commitSHA string, err error) {
	mirrorDir := inst.mirrorDir(ref.RepoURL)

	if err := inst.fetchMirror(ctx, mirrorDir); err != nil {
		return "", "", err
	}

	sha, err := inst.resolveCommit(ctx, mirrorDir, ref.Ref)
	if err != nil {
		return "", "", err
	}

	if sha == currentSHA {
		return inst.bundleDir(ref, sha), sha, nil
	}

	dest := inst.bundleDir(ref, sha)
	if _, err := os.Stat(dest); err == nil {
		return dest, sha, nil
	}

	if err := inst.materialize(ctx, mirrorDir, ref, sha, dest); err != nil {
		_ = os.RemoveAll(dest)
		return "", "", err
	}

	return dest, sha, nil
}

// syncMirror creates a bare mirror or fetches updates if it already exists.
func (inst *Installer) syncMirror(ctx context.Context, repoURL, mirrorDir string) error {
	if _, err := os.Stat(mirrorDir); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(mirrorDir), 0o755); err != nil {
			return err
		}
		// Partial clone: skip blob objects until they are needed.
		return inst.git(ctx, "",
			"clone", "--bare", "--filter=blob:none", repoURL, mirrorDir,
		)
	}
	return inst.fetchMirror(ctx, mirrorDir)
}

// fetchMirror fetches all branch and tag refs from origin into an existing
// bare mirror, explicitly updating packed-refs so that rev-parse always
// returns the latest upstream commit.
func (inst *Installer) fetchMirror(ctx context.Context, mirrorDir string) error {
	return inst.git(ctx, mirrorDir,
		"fetch", "--prune", "--force", "origin",
		"refs/heads/*:refs/heads/*",
		"refs/tags/*:refs/tags/*",
	)
}

// resolveCommit returns the full commit SHA for a ref inside a bare repo.
func (inst *Installer) resolveCommit(ctx context.Context, mirrorDir, ref string) (string, error) {
	out, err := inst.gitOutput(ctx, mirrorDir, "rev-parse", ref)
	if err != nil {
		return "", fmt.Errorf("source: cannot resolve ref %q: %w", ref, err)
	}
	sha := strings.TrimSpace(out)
	if len(sha) != 40 {
		return "", fmt.Errorf("source: unexpected rev-parse output %q", sha)
	}
	return sha, nil
}

// materialize checks out the bundle subdir from the bare mirror into dest.
func (inst *Installer) materialize(ctx context.Context, mirrorDir string, ref *Ref, sha, dest string) error {
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}

	// Use git worktree add for a clean checkout from a bare repo.
	// sparse-checkout is configured inside the worktree.
	worktree := dest + ".wt"
	defer os.RemoveAll(worktree)

	if err := inst.git(ctx, mirrorDir,
		"worktree", "add", "--no-checkout", worktree, sha,
	); err != nil {
		return fmt.Errorf("source: worktree add: %w", err)
	}
	defer inst.git(context.Background(), mirrorDir, "worktree", "remove", "--force", worktree) //nolint:errcheck

	if err := inst.git(ctx, worktree, "sparse-checkout", "init", "--cone"); err != nil {
		return fmt.Errorf("source: sparse-checkout init: %w", err)
	}

	if ref.Subdir != "" {
		if err := inst.git(ctx, worktree, "sparse-checkout", "set", ref.Subdir); err != nil {
			return fmt.Errorf("source: sparse-checkout set: %w", err)
		}
	}

	if err := inst.git(ctx, worktree, "checkout", sha); err != nil {
		return fmt.Errorf("source: checkout: %w", err)
	}

	// Copy only the bundle subdir (or whole worktree if no subdir) to dest.
	src := worktree
	if ref.Subdir != "" {
		src = filepath.Join(worktree, filepath.FromSlash(ref.Subdir))
	}

	if err := copyDir(src, dest); err != nil {
		return fmt.Errorf("source: copy bundle: %w", err)
	}

	return nil
}

// mirrorDir returns the path to the bare mirror for a repo URL.
func (inst *Installer) mirrorDir(repoURL string) string {
	h := sha256.Sum256([]byte(repoURL))
	return filepath.Join(inst.StateDir, "repos", hex.EncodeToString(h[:]))
}

// bundleDir returns the materialized bundle path for a given ref and SHA.
func (inst *Installer) bundleDir(ref *Ref, sha string) string {
	seg := repoSlug(ref.RepoURL)
	if ref.Subdir != "" {
		seg = filepath.Base(ref.Subdir)
	}
	return filepath.Join(inst.StateDir, "bundles", seg, sha)
}

// repoSlug extracts a short identifier from a repo URL.
func repoSlug(repoURL string) string {
	base := filepath.Base(repoURL)
	return strings.TrimSuffix(base, ".git")
}

// git runs a git command, inheriting stderr for visibility.
func (inst *Installer) git(ctx context.Context, dir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

// gitOutput runs a git command and returns its stdout.
func (inst *Installer) gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}
