// Package release builds deterministic, signed Spice release artifacts.
package release

import (
	"fmt"
	"runtime"
	"slices"
	"strings"
)

// Target identifies one supported CLI release platform.
type Target struct {
	GOOS   string
	GOARCH string
}

// String returns the canonical target spelling.
func (t Target) String() string {
	return t.GOOS + "/" + t.GOARCH
}

// ArchiveExtension returns the target's deterministic archive format.
func (t Target) ArchiveExtension() string {
	if t.GOOS == "windows" {
		return ".zip"
	}
	return ".tar.gz"
}

// ExecutableName returns the target's CLI executable name.
func (t Target) ExecutableName() string {
	if t.GOOS == "windows" {
		return "spice.exe"
	}
	return "spice"
}

// DefaultTargets returns the supported release matrix in stable order.
func DefaultTargets() []Target {
	return []Target{
		{GOOS: "darwin", GOARCH: "amd64"},
		{GOOS: "darwin", GOARCH: "arm64"},
		{GOOS: "linux", GOARCH: "amd64"},
		{GOOS: "linux", GOARCH: "arm64"},
		{GOOS: "windows", GOARCH: "amd64"},
		{GOOS: "windows", GOARCH: "arm64"},
	}
}

// HostTarget returns the current process target.
func HostTarget() Target {
	return Target{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
}

// ParseTargets decodes a comma-separated target list.
func ParseTargets(value string) ([]Target, error) {
	parts := strings.Split(value, ",")
	targets := make([]Target, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		targetParts := strings.Split(part, "/")
		if len(targetParts) != 2 {
			return nil, fmt.Errorf(
				"parse release target %q: require GOOS/GOARCH",
				part,
			)
		}
		target := Target{
			GOOS:   targetParts[0],
			GOARCH: targetParts[1],
		}
		if !slices.Contains(DefaultTargets(), target) {
			return nil, fmt.Errorf(
				"parse release target %q: unsupported target",
				part,
			)
		}
		if slices.Contains(targets, target) {
			return nil, fmt.Errorf(
				"parse release target %q: duplicate target",
				part,
			)
		}
		targets = append(targets, target)
	}
	slices.SortFunc(targets, func(left, right Target) int {
		return strings.Compare(left.String(), right.String())
	})
	return targets, nil
}
