package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	codegen "github.com/spice-framework/toolchain/compiler/generate"
	"github.com/spice-framework/toolchain/compiler/load"
)

func TestGeneratedCommandLocatesSourceAndGeneratedRanges(t *testing.T) {
	t.Parallel()
	root := generatedLookupFixture(t)
	source := filepath.Join(root, "orders", "service.go")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := run(
		[]string{
			"generated",
			"--source",
			source,
			"--line",
			"12",
			"--format",
			"json",
		},
		&stdout,
		&stderr,
		load.Options{Dir: root},
		load.Load,
	)
	if exitCode != 0 {
		t.Fatalf(
			"generated source lookup exit code = %d, stderr = %q",
			exitCode,
			stderr.String(),
		)
	}
	var result generatedQueryResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode generated source lookup: %v", err)
	}
	if result.Direction != "source-to-generated" ||
		len(result.Matches) != 1 ||
		result.Matches[0].Generated.Path !=
			"internal/spicegen/app/sources/orders/service_spice_gen.go" ||
		result.Matches[0].Generated.Line != 18 ||
		result.Matches[0].Source.Path != "orders/service.go" {
		t.Fatalf("generated source lookup = %#v", result)
	}

	stdout.Reset()
	stderr.Reset()
	exitCode = run(
		[]string{
			"generated",
			"--generated",
			filepath.Join(
				root,
				"internal",
				"spicegen",
				"app",
				"sources",
				"orders",
				"service_spice_gen.go",
			),
			"--line",
			"20",
		},
		&stdout,
		&stderr,
		load.Options{Dir: root},
		load.Load,
	)
	if exitCode != 0 {
		t.Fatalf(
			"generated reverse lookup exit code = %d, stderr = %q",
			exitCode,
			stderr.String(),
		)
	}
	if !strings.Contains(
		stdout.String(),
		"orders/service.go:12:4 -> "+
			"internal/spicegen/app/sources/orders/service_spice_gen.go:18:1",
	) {
		t.Fatalf("generated reverse lookup stdout = %q", stdout.String())
	}
}

func TestGeneratedCommandRejectsInvalidOrUnmappedQueries(t *testing.T) {
	t.Parallel()
	root := generatedLookupFixture(t)
	tests := []struct {
		name      string
		arguments []string
		wantCode  int
		wantError string
	}{
		{
			name:      "neither direction",
			arguments: []string{"generated"},
			wantCode:  2,
			wantError: "exactly one of --source or --generated is required",
		},
		{
			name: "both directions",
			arguments: []string{
				"generated",
				"--source",
				"orders/service.go",
				"--generated",
				"generated.go",
			},
			wantCode:  2,
			wantError: "exactly one of --source or --generated is required",
		},
		{
			name: "invalid line",
			arguments: []string{
				"generated",
				"--source",
				"orders/service.go",
				"--line",
				"zero",
			},
			wantCode:  2,
			wantError: "--line must be a positive integer",
		},
		{
			name: "outside module",
			arguments: []string{
				"generated",
				"--source",
				filepath.Dir(root),
			},
			wantCode:  1,
			wantError: "outside module",
		},
		{
			name: "unmapped source line",
			arguments: []string{
				"generated",
				"--source",
				filepath.Join(root, "orders", "service.go"),
				"--line",
				"13",
			},
			wantCode:  1,
			wantError: "no owned source mapping found",
		},
		{
			name: "unknown target",
			arguments: []string{
				"generated",
				"--source",
				filepath.Join(root, "orders", "service.go"),
				"--target",
				"missing",
			},
			wantCode:  1,
			wantError: "target \"missing\" has no ownership manifest",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			exitCode := run(
				test.arguments,
				&stdout,
				&stderr,
				load.Options{Dir: root},
				load.Load,
			)
			if exitCode != test.wantCode ||
				!strings.Contains(stderr.String(), test.wantError) {
				t.Fatalf(
					"run(%q) = %d, stderr = %q; want %d containing %q",
					test.arguments,
					exitCode,
					stderr.String(),
					test.wantCode,
					test.wantError,
				)
			}
		})
	}
}

func TestGeneratedCommandRejectsOversizedAndStaleManifests(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		content   []byte
		wantError string
	}{
		{
			name:      "oversized",
			content:   bytes.Repeat([]byte("x"), maxGeneratedManifestSize+1),
			wantError: "exceeds the",
		},
		{
			name: "stale schema",
			content: []byte(
				`{"schema":3,"target":{"id":"app"},"files":[]}`,
			),
			wantError: "uses schema 3",
		},
		{
			name:      "malformed",
			content:   []byte(`{`),
			wantError: "decode generated ownership manifest",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeGeneratedFixtureFile(t, root, "go.mod", "module example.com/app\n")
			writeGeneratedFixtureFile(
				t,
				root,
				".spice/app.manifest.json",
				string(test.content),
			)
			var stderr bytes.Buffer
			exitCode := run(
				[]string{
					"generated",
					"--source",
					filepath.Join(root, "orders", "service.go"),
				},
				&bytes.Buffer{},
				&stderr,
				load.Options{Dir: root},
				load.Load,
			)
			if exitCode != 1 ||
				!strings.Contains(stderr.String(), test.wantError) {
				t.Fatalf(
					"generated malformed lookup = %d, stderr %q",
					exitCode,
					stderr.String(),
				)
			}
		})
	}
}

func generatedLookupFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeGeneratedFixtureFile(t, root, "go.mod", "module example.com/app\n")
	writeGeneratedFixtureFile(
		t,
		root,
		"orders/service.go",
		"package orders\n",
	)
	manifest := codegen.Manifest{
		Schema: codegen.SchemaVersion,
		Target: codegen.TargetSummary{
			ID:           "app",
			ModulePath:   "example.com/app",
			OutputDir:    "internal/spicegen/app",
			ManifestPath: ".spice/app.manifest.json",
		},
		Files: []codegen.ManifestFile{{
			Path: "internal/spicegen/app/sources/orders/service_spice_gen.go",
			Role: codegen.FileRoleSourceUnit,
			PrimarySource: &codegen.SourceOrigin{
				Path:   "orders/service.go",
				Line:   12,
				Column: 4,
			},
			Mappings: []codegen.SourceMapping{{
				Kind:         "provider-constructor",
				Contribution: "orders.Service",
				Source: codegen.SourceOrigin{
					Path:   "orders/service.go",
					Line:   12,
					Column: 4,
				},
				Generated: codegen.GeneratedRange{
					StartLine:   18,
					StartColumn: 1,
					EndLine:     22,
					EndColumn:   2,
				},
			}},
		}},
	}
	content, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal generated manifest: %v", err)
	}
	writeGeneratedFixtureFile(
		t,
		root,
		".spice/app.manifest.json",
		string(content),
	)
	return root
}

func writeGeneratedFixtureFile(
	t *testing.T,
	root string,
	relativePath string,
	content string,
) {
	t.Helper()
	fullPath := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o750); err != nil {
		t.Fatalf("create fixture directory: %v", err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}
}
