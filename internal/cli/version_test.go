package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestWriteVersionIdentity(t *testing.T) {
	t.Parallel()
	const commit = "0123456789abcdef0123456789abcdef01234567"
	for _, test := range []struct {
		name, version, commit, want string
	}{
		{name: "development", version: developmentVersion, commit: developmentCommit, want: "spice v0.1.0-preview.5 (development)\n"},
		{name: "release", version: "0.1.0-preview.5", commit: commit, want: "spice 0.1.0-preview.5 (" + commit + ")\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var output bytes.Buffer
			if err := writeVersionIdentity(&output, test.version, test.commit); err != nil {
				t.Fatal(err)
			}
			if output.String() != test.want {
				t.Fatalf("identity = %q, want %q", output.String(), test.want)
			}
		})
	}
}

func TestWriteVersionIdentityRejectsInvalidLinkerBoundaries(t *testing.T) {
	t.Parallel()
	const commit = "0123456789abcdef0123456789abcdef01234567"
	for _, test := range []struct{ name, version, commit string }{
		{name: "unset version", commit: commit},
		{name: "unset commit", version: "0.1.0-preview.5"},
		{name: "release version with development commit", version: "0.1.0-preview.5", commit: developmentCommit},
		{name: "development version with release commit", version: developmentVersion, commit: commit},
		{name: "version has v prefix", version: "v0.1.0-preview.5", commit: commit},
		{name: "shorthand major", version: "1", commit: commit},
		{name: "shorthand minor", version: "1.2", commit: commit},
		{name: "version has build metadata", version: "0.1.0+local", commit: commit},
		{name: "version has whitespace", version: " 0.1.0", commit: commit},
		{name: "uppercase commit", version: "0.1.0", commit: "0123456789ABCDEF0123456789ABCDEF01234567"},
		{name: "non hexadecimal commit", version: "0.1.0", commit: "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"},
		{name: "short commit", version: "0.1.0", commit: "01234567"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := writeVersionIdentity(&bytes.Buffer{}, test.version, test.commit); err == nil {
				t.Fatal("writeVersionIdentity() error = nil")
			}
		})
	}
	if err := writeVersionIdentity(nil, developmentVersion, developmentCommit); err == nil {
		t.Fatal("nil writer was accepted")
	}
}

func TestWriteVersionIdentityPropagatesWriterFailure(t *testing.T) {
	t.Parallel()
	err := writeVersionIdentity(errorWriter{}, developmentVersion, developmentCommit)
	if err == nil || !strings.Contains(err.Error(), "write failed") {
		t.Fatalf("writeVersionIdentity() error = %v", err)
	}
}
