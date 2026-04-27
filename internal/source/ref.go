package source

import (
	"fmt"
	"net/url"
	"strings"
)

// Ref holds a parsed bundle source reference.
// Accepted forms:
//
//	https://host/org/repo.git
//	https://host/org/repo.git?ref=master&dir=models/resnet50
//	git+https://host/org/repo.git?ref=master&dir=models/resnet50
//	git+ssh://git@host/org/repo.git?ref=master&dir=models/resnet50
type Ref struct {
	// RepoURL is the clean Git remote URL (scheme without the git+ prefix).
	RepoURL string
	// Ref is the branch, tag, or full ref to check out. Defaults to "master".
	Ref string
	// Subdir is the path inside the repository that contains the bundle.
	// Empty string means the repository root.
	Subdir string
}

// ParseRef parses a raw source string into a Ref.
// The ref and dir parameters override query-string values when non-empty.
func ParseRef(raw, refOverride, dirOverride string) (*Ref, error) {
	// Strip git+ prefix so net/url can parse it.
	clean := strings.TrimPrefix(raw, "git+")

	u, err := url.Parse(clean)
	if err != nil {
		return nil, fmt.Errorf("source: invalid URL %q: %w", raw, err)
	}

	q := u.Query()

	// Extract ref and dir from query string, then strip them so the repo URL is clean.
	ref := q.Get("ref")
	dir := q.Get("dir")
	q.Del("ref")
	q.Del("dir")
	u.RawQuery = q.Encode()

	// CLI flags take precedence over query string.
	if refOverride != "" {
		ref = refOverride
	}
	if dirOverride != "" {
		dir = dirOverride
	}
	if ref == "" {
		ref = "master"
	}

	if u.Host == "" {
		return nil, fmt.Errorf("source: missing host in URL %q", raw)
	}

	return &Ref{
		RepoURL: u.String(),
		Ref:     ref,
		Subdir:  dir,
	}, nil
}

// String returns a canonical representation suitable for logs.
func (r *Ref) String() string {
	if r.Subdir != "" {
		return fmt.Sprintf("%s?ref=%s&dir=%s", r.RepoURL, r.Ref, r.Subdir)
	}
	return fmt.Sprintf("%s?ref=%s", r.RepoURL, r.Ref)
}
