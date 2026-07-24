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
	if code != 0 {
		t.Fatalf("Run() code = %d, want 0; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), Version) {
		t.Fatalf("stdout = %q, want version %q", stdout.String(), Version)
	}
}

func TestRunVerifyAcceptsValidBuiltIns(t *testing.T) {
	t.Parallel()

	root := writeGoSource(t, `package sample

// @Controller(prefix="/users")
type Controller struct{}

// @Get(path="/{id}")
func (Controller) Get() {}
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"verify", root}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("Run() code = %d, want 0; stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "verification passed") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestRunVerifyRejectsInvalidTarget(t *testing.T) {
	t.Parallel()

	root := writeGoSource(t, `package sample

// @Controller(prefix="/users")
func NewController() {}
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"verify", root}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("Run() code = %d, want 1", code)
	}
	message := stderr.String()
	for _, expected := range []string{"sample.go:3:1", "@Controller", "function \"NewController\"", "allowed target: type"} {
		if !strings.Contains(message, expected) {
			t.Fatalf("stderr = %q, missing %q", message, expected)
		}
	}
}

func TestRunVerifyRejectsUnknownAnnotation(t *testing.T) {
	t.Parallel()

	root := writeGoSource(t, `package sample

// @custom.Feature
func Feature() {}
`)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"verify", root}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("Run() code = %d, want 1", code)
	}
	if message := stderr.String(); !strings.Contains(message, "unknown annotation @custom.Feature") {
		t.Fatalf("stderr = %q", message)
	}
}

func TestRunUnknownCommand(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"missing"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("Run() code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func writeGoSource(t *testing.T, source string) string {
	t.Helper()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "sample.go"), []byte(source), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return root
}
