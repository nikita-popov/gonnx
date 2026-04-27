// Package source handles Git-based bundle installation and updates.
// It shells out to the system git binary to perform clone, fetch,
// sparse-checkout, and commit resolution.
// The canonical install flow:
//   1. resolve source identity: URL + ref + subdir
//   2. fetch/update local mirror under state/repos/<origin-hash>/
//   3. materialize bundle subdir into bundles/<name>/<commit-sha>/
//   4. resolve exact commit SHA
//   5. compute bundle digest
//   6. record in registry
package source

// Ref holds a parsed bundle source reference.
type Ref struct {
	URL    string
	Ref    string
	Subdir string
}
