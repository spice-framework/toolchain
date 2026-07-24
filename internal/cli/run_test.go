package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"verify", root}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "verification passed") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
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
			root := writeGoSource(t, test.source)
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := Run([]string{"verify", root}, &stdout, &stderr)
			if code != 1 {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
			for _, expected := range test.expected {
				if !strings.Contains(stderr.String(), expected) {
					t.Fatalf("stderr=%q missing=%q", stderr.String(), expected)
				}
			}
		})
	}
}

func TestRunVerifyRejectsInvalidTarget(t *testing.T) {
	t.Parallel()
	root := writeGoSource(t, "package sample\n\n// @Controller(prefix=\"/users\")\nfunc NewController() {}\n")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"verify", root}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "allowed target: type") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestRunVerifyRejectsUnknownAnnotation(t *testing.T) {
	t.Parallel()
	root := writeGoSource(t, "package sample\n\n// @custom.Feature\nfunc Feature() {}\n")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"verify", root}, &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "unknown annotation @custom.Feature") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
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

func writeGoSource(t *testing.T, source string) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}
