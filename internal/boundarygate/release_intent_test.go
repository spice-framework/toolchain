package boundarygate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseIntentIsCanonical(t *testing.T) {
	t.Parallel()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if err = (verifier{root: root}).releaseIntent(); err != nil {
		t.Fatal(err)
	}
}

func TestReleaseIntentFailsClosed(t *testing.T) {
	t.Parallel()
	valid := canonicalReleaseIntent(t)
	for _, test := range []struct {
		name   string
		mutate func(string) string
	}{
		{name: "schema", mutate: func(value string) string { return strings.Replace(value, `"schema": 1`, `"schema": 2`, 1) }},
		{name: "profile", mutate: func(value string) string { return strings.Replace(value, "go-distribution-v1", "go-module-v1", 1) }},
		{name: "repository", mutate: func(value string) string { return strings.Replace(value, `"toolchain"`, `"spice"`, 1) }},
		{name: "module", mutate: func(value string) string {
			return strings.Replace(value, "github.com/spice-framework/toolchain", "example.com/toolchain", 1)
		}},
		{name: "stale preview.6 version", mutate: func(value string) string { return strings.Replace(value, "v0.1.0-preview.7", "v0.1.0-preview.6", 1) }},
		{name: "unknown", mutate: func(value string) string { return strings.Replace(value, "\n}\n", ",\n  \"unknown\": true\n}\n", 1) }},
		{name: "trailing", mutate: func(value string) string { return value + "{}\n" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeGateFile(t, root, releaseIntentPath, test.mutate(valid))
			if err := (verifier{root: root}).releaseIntent(); err == nil {
				t.Fatal("mutated release intent succeeded")
			}
		})
	}
}

func TestReleaseIntentRejectsMissingDirectorySymlinkAndOversizedInput(t *testing.T) {
	t.Parallel()
	t.Run("missing", func(t *testing.T) {
		t.Parallel()
		if err := (verifier{root: t.TempDir()}).releaseIntent(); err == nil {
			t.Fatal("missing release intent succeeded")
		}
	})
	t.Run("directory", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		if err := os.Mkdir(filepath.Join(root, releaseIntentPath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := (verifier{root: root}).releaseIntent(); err == nil {
			t.Fatal("release intent directory succeeded")
		}
	})
	t.Run("symlink", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		target := filepath.Join(root, "target.json")
		if err := os.WriteFile(target, []byte(canonicalReleaseIntent(t)), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(root, releaseIntentPath)); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		if err := (verifier{root: root}).releaseIntent(); err == nil {
			t.Fatal("release intent symlink succeeded")
		}
	})
	t.Run("oversized", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeGateFile(t, root, releaseIntentPath, strings.Repeat(" ", maximumReleaseIntentBytes+1))
		if err := (verifier{root: root}).releaseIntent(); err == nil {
			t.Fatal("oversized release intent succeeded")
		}
	})
}

func canonicalReleaseIntent(t *testing.T) string {
	t.Helper()
	content, err := json.MarshalIndent(expectedReleaseIntent(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return string(content) + "\n"
}

func writeValidReleaseIntent(t *testing.T, root string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, releaseIntentPath), []byte(canonicalReleaseIntent(t)), 0o600); err != nil {
		t.Fatal(err)
	}
}
