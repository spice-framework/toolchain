package cli

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/mod/semver"
)

const (
	developmentVersion = "v0.1.0-preview.5"
	developmentCommit  = "development"
)

// Version and Commit are direct string variables so the release builder can
// replace both values through Go's -X linker assignment. Source builds retain
// an explicit development commit rather than claiming an immutable release.
var (
	Version = developmentVersion
	Commit  = developmentCommit
)

func writeVersionIdentity(writer io.Writer, version, commit string) error {
	if writer == nil {
		return errors.New("version output is unavailable")
	}
	if err := validateVersionIdentity(version, commit); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(writer, "spice %s (%s)\n", version, commit); err != nil {
		return fmt.Errorf("write Spice version identity: %w", err)
	}
	return nil
}

func validateVersionIdentity(version, commit string) error {
	if version == developmentVersion || commit == developmentCommit {
		if version == developmentVersion && commit == developmentCommit {
			return nil
		}
		return errors.New("development version and commit must be selected together")
	}
	semanticVersion := "v" + version
	if version == "" || version != strings.TrimSpace(version) || strings.HasPrefix(version, "v") ||
		!semver.IsValid(semanticVersion) || semver.Canonical(semanticVersion) != semanticVersion ||
		semver.Build(semanticVersion) != "" {
		return errors.New("release version must be canonical SemVer without a v prefix or build metadata")
	}
	if !validReleaseCommit(commit) {
		return errors.New("release commit must be exactly 40 lowercase hexadecimal characters")
	}
	return nil
}

func validReleaseCommit(value string) bool {
	if len(value) != 40 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 20
}
