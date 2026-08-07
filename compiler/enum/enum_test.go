package enum

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/spice-framework/toolchain/compiler/load"
	"github.com/spice-framework/toolchain/compiler/resolve"
	"github.com/spice-framework/toolchain/internal/testannotation"
)

func TestBuildValidatesClosedEnum(t *testing.T) {
	program, resolution := loadEnumFixture(t, map[string]string{
		"status.go": `package app

// @Enum
type OrderStatus string

const (
	OrderStatusPending OrderStatus = "pending"
	OrderStatusPaid OrderStatus = "paid"
)
`,
	})
	catalog := Build(program, resolution)
	if diagnostics := catalog.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("Build() diagnostics = %v", diagnosticStrings(diagnostics))
	}
	types := catalog.Types()
	if len(types) != 1 ||
		types[0].Name != "OrderStatus" ||
		types[0].TypeID != "example.com/enums/app.OrderStatus" {
		t.Fatalf("Types() = %#v", types)
	}
	members := types[0].Members()
	if len(members) != 2 ||
		members[0].Name != "OrderStatusPending" ||
		members[0].Value != `"pending"` ||
		members[1].Name != "OrderStatusPaid" ||
		members[1].Value != `"paid"` {
		t.Fatalf("Members() = %#v", members)
	}
	members[0].Name = "changed"
	if catalog.Types()[0].Members()[0].Name != "OrderStatusPending" {
		t.Fatal("Types() did not return defensive member metadata")
	}
}

func TestBuildRejectsInvalidEnumStructure(t *testing.T) {
	tests := []struct {
		name  string
		files map[string]string
		want  []string
	}{
		{
			name: "unsupported type",
			files: map[string]string{
				"value.go": "package app\n\n// @Enum\ntype Value float64\n\nconst ValueOne Value = 1\n",
			},
			want: []string{"must have a string or integer underlying type"},
		},
		{
			name: "missing members",
			files: map[string]string{
				"value.go": "package app\n\n// @Enum\ntype Value string\n",
			},
			want: []string{"requires at least one same-file constant"},
		},
		{
			name: "unrelated constant",
			files: map[string]string{
				"value.go": "package app\n\n// @Enum\ntype Value string\n\nconst ValueOne Value = \"one\"\nconst Other = 1\n",
			},
			want: []string{"every constant in an enum file must use exact enum type"},
		},
		{
			name: "member in other file",
			files: map[string]string{
				"value.go":      "package app\n\n// @Enum\ntype Value string\n\nconst ValueOne Value = \"one\"\n",
				"additional.go": "package app\n\nconst ValueTwo Value = \"two\"\n",
			},
			want: []string{"is declared outside value.go"},
		},
		{
			name: "duplicate value",
			files: map[string]string{
				"value.go": "package app\n\n// @Enum\ntype Value int\n\nconst (\n ValueOne Value = 1\n ValueAgain Value = 1\n)\n",
			},
			want: []string{"duplicates underlying value 1"},
		},
		{
			name: "two enums",
			files: map[string]string{
				"value.go": "package app\n\n// @Enum\ntype First string\nconst FirstOne First = \"one\"\n\n// @Enum\ntype Second string\nconst SecondOne Second = \"one\"\n",
			},
			want: []string{"enum files declare exactly one enum type"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			program, resolution := loadEnumFixture(t, test.files)
			diagnostics := Build(program, resolution).Diagnostics()
			for _, wanted := range test.want {
				if !containsDiagnostic(diagnostics, wanted) {
					t.Fatalf(
						"Build() diagnostics = %v, want %q",
						diagnosticStrings(diagnostics),
						wanted,
					)
				}
			}
		})
	}
}

func TestBuildRejectsNilProgram(t *testing.T) {
	diagnostics := Build(nil, resolve.Result{}).Diagnostics()
	if len(diagnostics) != 1 ||
		!strings.Contains(diagnostics[0].Message, "requires a loaded program") {
		t.Fatalf("Build(nil) diagnostics = %#v", diagnostics)
	}
}

func loadEnumFixture(
	t *testing.T,
	files map[string]string,
) (*load.Program, resolve.Result) {
	t.Helper()
	root := t.TempDir()
	allFiles := map[string]string{
		"go.mod": "module example.com/enums\n\ngo 1.26.0\n",
	}
	for name, content := range files {
		allFiles[filepath.ToSlash(filepath.Join("app", name))] = content
	}
	paths := make([]string, 0, len(allFiles))
	for name := range allFiles {
		paths = append(paths, name)
	}
	sort.Strings(paths)
	for _, name := range paths {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(allFiles[name]), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	program, err := load.Load(
		context.Background(),
		load.Options{Dir: root},
		"./app",
	)
	if err != nil {
		t.Fatalf("load.Load() error = %v", err)
	}
	resolution := resolve.Annotations(program)
	if len(resolution.Diagnostics) != 0 {
		t.Fatalf("resolve.Annotations() diagnostics = %v", resolution.Diagnostics)
	}
	resolution, err = testannotation.AttachOfficial(resolution)
	if err != nil {
		t.Fatalf("AttachOfficial() error = %v", err)
	}
	return program, resolution
}

func containsDiagnostic(diagnostics []Diagnostic, wanted string) bool {
	for _, diagnostic := range diagnostics {
		if strings.Contains(diagnostic.Message, wanted) {
			return true
		}
	}
	return false
}

func diagnosticStrings(diagnostics []Diagnostic) []string {
	result := make([]string, len(diagnostics))
	for index, diagnostic := range diagnostics {
		result[index] = diagnostic.Error()
	}
	return result
}
