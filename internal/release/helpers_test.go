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
	t.Setenv("GO111MODULE", "off")
	t.Setenv("CGO_ENABLED", "1")
	t.Setenv("GOPROXY", "https://proxy.example")
	t.Setenv("GOSUMDB", "sum.example")
	t.Setenv("GOPRIVATE", "private.example")
	t.Setenv("GONOPROXY", "noproxy.example")
	t.Setenv("GONOSUMDB", "nosum.example")
	t.Setenv("GOENV", "environment-file")
	t.Setenv("GOFLAGS", "-tags=ambient")
	t.Setenv("GOAMD64", "v3")
	t.Setenv("GOARM64", "v9.4")
	t.Setenv("GOEXPERIMENT", "ambient-experiment")
	t.Setenv("GOFIPS140", "latest")
	t.Setenv("GODEBUG", "gocacheverify=1")
	t.Setenv("GOTOOLCHAIN", "auto")
	environment := releaseEnvironment("linux", "arm64")
	joined := strings.Join(environment, "\n")
	for _, want := range []string{
		"GOOS=linux",
		"GOARCH=arm64",
		"GO111MODULE=on",
		"CGO_ENABLED=0",
		"GODEBUG=",
		"GOENV=off",
		"GOEXPERIMENT=",
		"GOFIPS140=off",
		"GOFLAGS=",
		"GOARM64=v8.0",
		"GONOPROXY=",
		"GONOSUMDB=",
		"GOPRIVATE=",
		"GOPROXY=off",
		"GOSUMDB=off",
		"GOTOOLCHAIN=local",
		"GOWORK=off",
	} {
		if strings.Count(joined, want) != 1 {
			t.Errorf("environment has %d occurrences of %q", strings.Count(joined, want), want)
		}
	}
	if strings.Contains(joined, "forbidden") ||
		strings.Contains(joined, "proxy.example") ||
		strings.Contains(joined, "sum.example") ||
		strings.Contains(joined, "private.example") ||
		strings.Contains(joined, "environment-file") ||
		strings.Contains(joined, "ambient") ||
		strings.Contains(joined, "v9.4") ||
		strings.Contains(joined, "latest") ||
		strings.Contains(joined, "gocacheverify") {
		t.Fatalf("environment retained an ambient build control: %s", joined)
	}
}

func TestListModulesRequiresExactVendoredGraph(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		goMod       string
		vendor      string
		wantError   string
		wantModules int
	}{
		{
			name: "exact",
			goMod: "module github.com/spice-framework/toolchain\n\ngo 1.26.0\n\n" +
				"require example.com/dependency v1.2.3\n",
			vendor: "# example.com/dependency v1.2.3\n" +
				"## explicit; go 1.26.0\nexample.com/dependency/pkg\n",
			wantModules: 2,
		},
		{
			name:      "wrong module",
			goMod:     "module example.com/wrong\n\ngo 1.26.0\n",
			wantError: "require \"github.com/spice-framework/toolchain\"",
		},
		{
			name: "replace",
			goMod: "module github.com/spice-framework/toolchain\n\ngo 1.26.0\n\n" +
				"replace example.com/dependency => ../dependency\n",
			wantError: "replace directives are forbidden",
		},
		{
			name: "missing",
			goMod: "module github.com/spice-framework/toolchain\n\ngo 1.26.0\n\n" +
				"require example.com/dependency v1.2.3\n",
			wantError: "is missing",
		},
		{
			name: "mismatch",
			goMod: "module github.com/spice-framework/toolchain\n\ngo 1.26.0\n\n" +
				"require example.com/dependency v1.2.3\n",
			vendor: "# example.com/dependency v1.2.4\n" +
				"## explicit; go 1.26.0\n",
			wantError: "is v1.2.4, require v1.2.3",
		},
		{
			name: "implicit",
			goMod: "module github.com/spice-framework/toolchain\n\ngo 1.26.0\n\n" +
				"require example.com/dependency v1.2.3\n",
			vendor: "# example.com/dependency v1.2.3\n" +
				"## go 1.26.0\n",
			wantError: "is not explicit",
		},
		{
			name:  "undeclared",
			goMod: "module github.com/spice-framework/toolchain\n\ngo 1.26.0\n",
			vendor: "# example.com/dependency v1.2.3\n" +
				"## explicit; go 1.26.0\n",
			wantError: "undeclared module",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(test.goMod), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(root, "vendor"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(
				filepath.Join(root, "vendor", "modules.txt"),
				[]byte(test.vendor),
				0o600,
			); err != nil {
				t.Fatal(err)
			}
			modules, err := listModules(t.Context(), root)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("listModules() error = %v, want %q", err, test.wantError)
				}
				return
			}
			if err != nil || len(modules) != test.wantModules {
				t.Fatalf("listModules() = %#v, %v", modules, err)
			}
		})
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
			line: "example.com/module v1.2.3 => example.com/fork v1.4.0",
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
