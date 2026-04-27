// Package sysdeps checks system-level dependencies declared in a bundle
// manifest. These are binaries or libraries that must be installed on the
// host (e.g. espeak-ng) and cannot be pulled via pip.
//
// Two entry-points are provided:
//
//	Warnings — used at pull time; returns human-readable lines for each
//	           missing dep (non-fatal, printed as NDJSON warnings).
//
//	Check    — used at load time; returns a slice of missing-dep descriptions
//	           that the caller uses to mark the worker as degraded.
package sysdeps

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/nikita-popov/gonnx/internal/bundle"
)

// Missing describes one unresolved system dependency.
type Missing struct {
	Name string
	Hint string
}

func (m Missing) String() string {
	if m.Hint != "" {
		return fmt.Sprintf("%s (hint: %s)", m.Name, m.Hint)
	}
	return m.Name
}

// Check runs every check command declared in m.System.Deps and returns the
// list of deps whose check command exits non-zero or is not found in PATH.
// An empty slice means all deps are satisfied.
func Check(deps []bundle.SystemDep) []Missing {
	var missing []Missing
	for _, d := range deps {
		if !present(d.Check) {
			missing = append(missing, Missing{Name: d.Name, Hint: d.Hint})
		}
	}
	return missing
}

// Warnings is like Check but returns ready-to-print warning strings.
// Intended for streaming to the client during pull.
func Warnings(deps []bundle.SystemDep) []string {
	missing := Check(deps)
	if len(missing) == 0 {
		return nil
	}
	out := make([]string, 0, len(missing))
	for _, m := range missing {
		out = append(out, fmt.Sprintf("missing system dependency: %s", m))
	}
	return out
}

// present runs the check command and returns true if it exits 0.
func present(checkCmd string) bool {
	if checkCmd == "" {
		return true
	}
	parts := strings.Fields(checkCmd)
	if len(parts) == 0 {
		return true
	}
	cmd := exec.Command(parts[0], parts[1:]...) //nolint:gosec
	err := cmd.Run()
	return err == nil
}
