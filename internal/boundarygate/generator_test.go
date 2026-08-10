package boundarygate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spice-framework/toolchain/compiler/generate"
)

func TestGeneratorCompatibilityContractIsCanonicalAndCrossChecked(t *testing.T) {
	t.Parallel()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if err := (verifier{root: root}).generatorCompatibility(); err != nil {
		t.Fatal(err)
	}
	if generate.GeneratorVersion != "v0.1.0-preview.2" ||
		generate.SchemaVersion != 6 {
		t.Fatalf(
			"generator identity = %s schema %d",
			generate.GeneratorVersion,
			generate.SchemaVersion,
		)
	}
}

func TestGeneratorCompatibilityContractFailsClosed(t *testing.T) {
	t.Parallel()
	valid := canonicalGeneratorContract(t)
	tests := []struct {
		name   string
		mutate func(string) string
	}{
		{
			name: "generator version",
			mutate: func(value string) string {
				return strings.Replace(value, "v0.1.0-preview.2", "v0.1.0-preview.1", 1)
			},
		},
		{
			name: "schema five migration",
			mutate: func(value string) string {
				return strings.Replace(value, "    5,\n", "", 1)
			},
		},
		{
			name: "manual edit policy",
			mutate: func(value string) string {
				return strings.Replace(value, `"manual_edit_policy": "reject"`, `"manual_edit_policy": "overwrite"`, 1)
			},
		},
		{
			name: "unknown field",
			mutate: func(value string) string {
				return strings.Replace(value, "\n}\n", ",\n  \"unknown\": true\n}\n", 1)
			},
		},
		{
			name:   "trailing JSON",
			mutate: func(value string) string { return value + "{}\n" },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeGateFile(t, root, generatorCompatibilityPath, test.mutate(valid))
			if err := (verifier{root: root}).generatorCompatibility(); err == nil {
				t.Fatal("mutated generator compatibility contract succeeded")
			}
		})
	}
}

func TestGeneratorCompatibilityContractRejectsOversizedInput(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeGateFile(
		t,
		root,
		generatorCompatibilityPath,
		strings.Repeat(" ", maximumGeneratorContractBytes+1),
	)
	if err := (verifier{root: root}).generatorCompatibility(); err == nil ||
		!strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized generator contract error = %v", err)
	}
}

func TestCommittedGeneratorContractMatchesTestEncoder(t *testing.T) {
	t.Parallel()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(generatorCompatibilityPath)))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != canonicalGeneratorContract(t) {
		t.Fatal("committed generator contract is not canonical")
	}
}

func writeValidGeneratorContract(t *testing.T, root string) {
	t.Helper()
	writeGateFile(t, root, generatorCompatibilityPath, canonicalGeneratorContract(t))
}

func canonicalGeneratorContract(t *testing.T) string {
	t.Helper()
	content, err := json.MarshalIndent(expectedGeneratorCompatibility(), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return string(content) + "\n"
}
