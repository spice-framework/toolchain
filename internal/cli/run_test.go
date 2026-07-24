package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/StevenBuglione/spice/compiler/load"
)

func TestRunVersion(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"version"}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), Version) {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestRunVerifyAcceptsValidBuiltIns(t *testing.T) {
	t.Parallel()
	root := writeGoSource(t, `package sample

// @Controller(prefix="/users")
type Controller struct{}

// @Get("/{id}")
func (Controller) Get() {}
`)
	code, stdout, stderr := runModule(root, "verify", ".")
	if code != 0 || !strings.Contains(stdout, "verification passed") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestRunLoadsOnceAndPassesPatternsUnchanged(t *testing.T) {
	t.Parallel()
	root := writeModule(t, map[string]string{
		"a/a.go": "package a\n\n// @Service\ntype A struct{}\n",
		"b/b.go": "package b\n\n// @Service\ntype B struct{}\n",
	})
	calls := 0
	var received []string
	loader := func(ctx context.Context, options load.Options, patterns ...string) (*load.Program, error) {
		calls++
		received = append([]string(nil), patterns...)
		return load.Load(ctx, options, patterns...)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"annotations", "./a", "./b"}, &stdout, &stderr, load.Options{Dir: root}, loader)
	if code != 0 || calls != 1 || !reflect.DeepEqual(received, []string{"./a", "./b"}) {
		t.Fatalf("code=%d calls=%d patterns=%v stdout=%q stderr=%q", code, calls, received, stdout.String(), stderr.String())
	}
}

func TestRunVerifyIgnoresBuildTagExcludedAnnotation(t *testing.T) {
	t.Parallel()
	root := writeModule(t, map[string]string{
		"active.go": "package sample\n\n// @Service\ntype Active struct{}\n",
		"excluded.go": `//go:build spice_never

package sample

// @UnknownAnnotation
func Excluded() {}
`,
	})
	code, stdout, stderr := runModule(root, "verify", ".")
	if code != 0 || !strings.Contains(stdout, "1 annotations") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestRunVerifyRejectsResolutionFailures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, source, expected string
	}{
		{"grouped", "package sample\n\n// @Configuration\ntype ( A struct{}; B struct{} )\n", "grouped declaration with 2 specs"},
		{"multi-name", "package sample\n\n// @Service\nvar Primary, Fallback int\n", "value declaration with 2 names"},
		{"blank", "package sample\n\n// @Service\nvar _ int\n", "cannot target blank identifier _"},
		{"malformed", "package sample\n\n// @Controller(prefix=)\ntype Controller struct{}\n", "unsupported argument value"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			code, stdout, stderr := runModule(writeGoSource(t, test.source), "verify", ".")
			if code != 1 || !strings.Contains(stderr, test.expected) {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
		})
	}
}

func TestRunVerifyRejectsArgumentFailures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		source   string
		expected []string
	}{
		{name: "missing", source: "package sample\n\n// @Get\nfunc (Controller) Get() {}\ntype Controller struct{}\n", expected: []string{`requires argument "path"`}},
		{name: "unknown", source: "package sample\n\n// @Controller(prefx=\"/users\")\ntype Controller struct{}\n", expected: []string{`does not define argument "prefx"`, "available argument: prefix"}},
		{name: "wrong kind", source: "package sample\n\n// @Controller(prefix=3)\ntype Controller struct{}\n", expected: []string{`argument "prefix" requires string, got integer`}},
		{name: "duplicate", source: "package sample\n\ntype Controller struct{}\n\n// @Get(\"/{id}\", path=\"/{other}\")\nfunc (Controller) Get() {}\n", expected: []string{`assigns argument "path" more than once`}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			code, stdout, stderr := runModule(writeGoSource(t, test.source), "verify", ".")
			if code != 1 {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
			for _, expected := range test.expected {
				if !strings.Contains(stderr, expected) {
					t.Fatalf("stderr=%q missing=%q", stderr, expected)
				}
			}
		})
	}
}

func TestRunVerifyRejectsInvalidTargetAndUnknownAnnotation(t *testing.T) {
	t.Parallel()
	tests := []struct{ source, expected string }{
		{"package sample\n\n// @Controller(prefix=\"/users\")\nfunc NewController() {}\n", "allowed target: type"},
		{"package sample\n\n// @custom.Feature\nfunc Feature() {}\n", "unknown annotation @custom.Feature"},
	}
	for _, test := range tests {
		code, _, stderr := runModule(writeGoSource(t, test.source), "verify", ".")
		if code != 1 || !strings.Contains(stderr, test.expected) {
			t.Fatalf("code=%d stderr=%q", code, stderr)
		}
	}
}

func TestRunUsesDisplayPositionAndStableOutput(t *testing.T) {
	t.Parallel()
	root := writeGoSource(t, "package sample\n\n//line generated/schema.go:40\n// @Service\ntype Service struct{}\n")
	var first string
	for run := 0; run < 10; run++ {
		code, stdout, stderr := runModule(root, "annotations", ".")
		if code != 0 || !strings.Contains(filepath.ToSlash(stdout), "generated/schema.go:40 type Service @Service") {
			t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
		}
		if run == 0 {
			first = stdout
		} else if stdout != first {
			t.Fatalf("output changed: first=%q next=%q", first, stdout)
		}
	}
}

func TestRunVerifyPreservesPhysicalDiagnosticOrderAcrossLineDirectives(t *testing.T) {
	t.Parallel()
	root := writeGoSource(t, `package sample

//line z.go:100
// @UnknownA
var First int

//line a.go:1
// @UnknownB
var Second int
`)
	var first string
	for run := 0; run < 10; run++ {
		code, stdout, stderr := runModule(root, "verify", ".")
		if code != 1 || stdout != "" {
			t.Fatalf("run=%d code=%d stdout=%q stderr=%q", run, code, stdout, stderr)
		}
		normalized := filepath.ToSlash(stderr)
		zIndex := strings.Index(normalized, "z.go:100:1: unknown annotation @UnknownA")
		aIndex := strings.Index(normalized, "a.go:1:1: unknown annotation @UnknownB")
		if zIndex < 0 || aIndex < 0 || zIndex >= aIndex {
			t.Fatalf("run=%d diagnostics not in physical order: %q", run, stderr)
		}
		if run == 0 {
			first = stderr
		} else if stderr != first {
			t.Fatalf("run=%d output changed: first=%q next=%q", run, first, stderr)
		}
	}
}

func TestRunFailsBeforeValidationForBrokenOrMissingPackage(t *testing.T) {
	t.Parallel()
	root := writeGoSource(t, "package sample\n\n// @UnknownAnnotation\nvar Value string = 1\n")
	code, _, stderr := runModule(root, "verify", ".")
	if code != 1 || !strings.Contains(stderr, "cannot use") || strings.Contains(stderr, "unknown annotation") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
	code, _, stderr = runModule(root, "verify", "./missing")
	if code != 1 || !strings.Contains(stderr, "directory not found") {
		t.Fatalf("code=%d stderr=%q", code, stderr)
	}
}

func TestRunUnknownCommand(t *testing.T) {
	t.Parallel()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := Run([]string{"missing"}, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func runModule(root string, arguments ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(arguments, &stdout, &stderr, load.Options{Dir: root}, load.Load)
	return code, stdout.String(), stderr.String()
}

func writeGoSource(t *testing.T, source string) string {
	t.Helper()
	return writeModule(t, map[string]string{"sample.go": source})
}

func writeModule(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/fixture\n\ngo 1.23.0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for path, source := range files {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
