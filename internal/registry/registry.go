// Package registry maintains a local SQLite database of installed bundles.
//
// Each row in the bundles table records:
//   - name        — canonical bundle name from manifest
//   - source_url  — clean repo URL (without ref/dir)
//   - source_ref  — branch/tag/ref
//   - source_dir  — subdir inside the repo
//   - commit_sha  — resolved 40-char SHA at install time
//   - bundle_dir  — absolute path to the materialized bundle on disk
//   - digest      — sha256 of the bundle directory contents
//   - installed_at — unix timestamp (seconds)
package registry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // register "sqlite" driver
)

const schema = `
CREATE TABLE IF NOT EXISTS bundles (
    name         TEXT NOT NULL PRIMARY KEY,
    source_url   TEXT NOT NULL,
    source_ref   TEXT NOT NULL,
    source_dir   TEXT NOT NULL DEFAULT '',
    commit_sha   TEXT NOT NULL,
    bundle_dir   TEXT NOT NULL,
    digest       TEXT NOT NULL,
    installed_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_bundles_commit ON bundles(commit_sha);
`

// Entry is a single registry record.
type Entry struct {
	Name        string
	SourceURL   string
	SourceRef   string
	SourceDir   string
	CommitSHA   string
	BundleDir   string
	Digest      string
	InstalledAt time.Time
}

// Registry wraps a SQLite database for bundle metadata storage.
type Registry struct {
	db *sql.DB
}

// Open opens (or creates) the registry database at path.
// It creates all parent directories as needed.
func Open(path string) (*Registry, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("registry: mkdir: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("registry: open db: %w", err)
	}

	// Single writer to avoid SQLITE_BUSY on concurrent daemon restarts.
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("registry: migrate: %w", err)
	}

	return &Registry{db: db}, nil
}

// Close closes the underlying database connection.
func (r *Registry) Close() error {
	return r.db.Close()
}

// Upsert inserts or replaces a bundle entry.
func (r *Registry) Upsert(ctx context.Context, e *Entry) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO bundles
		    (name, source_url, source_ref, source_dir, commit_sha, bundle_dir, digest, installed_at)
		VALUES
		    (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(name) DO UPDATE SET
		    source_url   = excluded.source_url,
		    source_ref   = excluded.source_ref,
		    source_dir   = excluded.source_dir,
		    commit_sha   = excluded.commit_sha,
		    bundle_dir   = excluded.bundle_dir,
		    digest       = excluded.digest,
		    installed_at = excluded.installed_at
	`,
		e.Name, e.SourceURL, e.SourceRef, e.SourceDir,
		e.CommitSHA, e.BundleDir, e.Digest,
		e.InstalledAt.Unix(),
	)
	if err != nil {
		return fmt.Errorf("registry: upsert %q: %w", e.Name, err)
	}
	return nil
}

// Get returns the entry for name or ErrNotFound.
func (r *Registry) Get(ctx context.Context, name string) (*Entry, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT name, source_url, source_ref, source_dir,
		       commit_sha, bundle_dir, digest, installed_at
		FROM bundles WHERE name = ?
	`, name)
	return scanEntry(row)
}

// List returns all registered bundles ordered by name.
func (r *Registry) List(ctx context.Context) ([]*Entry, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT name, source_url, source_ref, source_dir,
		       commit_sha, bundle_dir, digest, installed_at
		FROM bundles ORDER BY name
	`)
	if err != nil {
		return nil, fmt.Errorf("registry: list: %w", err)
	}
	defer rows.Close()

	var entries []*Entry
	for rows.Next() {
		e, err := scanEntry(rows)
		if err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, rows.Err()
}

// Delete removes the entry for name. It is not an error if name does not exist.
func (r *Registry) Delete(ctx context.Context, name string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM bundles WHERE name = ?`, name)
	if err != nil {
		return fmt.Errorf("registry: delete %q: %w", name, err)
	}
	return nil
}

// ErrNotFound is returned by Get when no entry exists for the given name.
var ErrNotFound = errors.New("registry: bundle not found")

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanEntry(s scanner) (*Entry, error) {
	var e Entry
	var ts int64
	err := s.Scan(
		&e.Name, &e.SourceURL, &e.SourceRef, &e.SourceDir,
		&e.CommitSHA, &e.BundleDir, &e.Digest, &ts,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("registry: scan: %w", err)
	}
	e.InstalledAt = time.Unix(ts, 0).UTC()
	return &e, nil
}
