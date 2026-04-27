// Package registry manages the local database of installed bundles.
// Each entry records origin URL, configured ref, resolved commit SHA,
// bundle subdirectory, install timestamp, manifest digest, and trust policy.
package registry

// Entry represents a single installed bundle revision.
type Entry struct {
	Name           string `db:"name"`
	OriginURL      string `db:"origin_url"`
	Ref            string `db:"ref"`
	CommitSHA      string `db:"commit_sha"`
	Subdir         string `db:"subdir"`
	InstalledAt    string `db:"installed_at"`
	ManifestDigest string `db:"manifest_digest"`
	BundleDigest   string `db:"bundle_digest"`
	TrustLevel     string `db:"trust_level"`
	Active         bool   `db:"active"`
}
