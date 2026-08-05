package release

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReleaseEnvironmentReplacesBuildControls(t *testing.T) {
	t.Setenv("GOOS", "forbidden")
	t.Setenv("GOARCH", "forbidden")
	t.Setenv("CGO_ENABLED", "1")
	t.Setenv("GOPROXY", "https://proxy.example")
	t.Setenv("GOTOOLCHAIN", "auto")
	environment := releaseEnvironment("linux", "arm64")
	joined := strings.Join(environment, "\n")
	for _, want := range []string{
		"GOOS=linux",
		"GOARCH=arm64",
		"CGO_ENABLED=0",
		"GOPROXY=off",
		"GOTOOLCHAIN=local",
		"GOWORK=off",
	} {
		if strings.Count(joined, want) != 1 {
			t.Errorf("environment has %d occurrences of %q", strings.Count(joined, want), want)
		}
	}
	if strings.Contains(joined, "forbidden") ||
		strings.Contains(joined, "proxy.example") {
		t.Fatalf("environment retained an ambient build control: %s", joined)
	}
}

func TestScopedReadAndBoundedDiagnostics(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(root, "value.txt"),
		[]byte("value"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	data, err := readScopedFile(root, "value.txt")
	if err != nil || string(data) != "value" {
		t.Fatalf("readScopedFile() = %q, %v", data, err)
	}
	if _, err := readScopedFile(root, "../outside"); err == nil {
		t.Fatal("scoped traversal was accepted")
	}
	long := append(
		make([]byte, maxDiagnosticBytes+10),
		[]byte("tail")...,
	)
	if got := boundedText(long); !strings.HasSuffix(got, "tail") ||
		len(got) > maxDiagnosticBytes {
		t.Fatalf("boundedText() length=%d suffix=%q", len(got), got[len(got)-4:])
	}
}

func TestParseVendoredModuleReplacements(t *testing.T) {
	t.Parallel()
	tests := []struct {
		line        string
		wantPath    string
		wantVersion string
		found       bool
	}{
		{
			line:        "example.com/module v1.2.3",
			wantPath:    "example.com/module",
			wantVersion: "v1.2.3",
			found:       true,
		},
		{
			line:        "example.com/module v1.2.3 => example.com/fork v1.4.0",
			wantPath:    "example.com/module",
			wantVersion: "v1.4.0",
			found:       true,
		},
		{line: "example.com/local => ../local"},
	}
	for _, test := range tests {
		module, found := parseVendoredModule(test.line)
		if found != test.found ||
			module.Path != test.wantPath ||
			module.Version != test.wantVersion {
			t.Errorf(
				"parseVendoredModule(%q) = %#v, %v",
				test.line,
				module,
				found,
			)
		}
	}
}
