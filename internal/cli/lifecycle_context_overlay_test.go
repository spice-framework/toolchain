package cli

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spice-framework/toolchain/compiler/load"
)

func TestRunVerifyRejectsWrongShapeCanonicalContextOverlay(t *testing.T) {
	root := writeModule(t, map[string]string{
		"app.go": `package sample
import "context"
type Server struct{}
// @Bean
func ServerProvider() Server { panic("provider must not execute") }
// @OnStart
func (Server) Boot(context.Context) error { panic("hook must not execute") }
`,
	})
	contextSource := filepath.Join(cliGoRoot(t), "src", "context", "context.go")
	options := load.Options{
		Dir:     root,
		Overlay: map[string][]byte{contextSource: []byte("package context\n\ntype Context string\n")},
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"verify", "."}, &stdout, &stderr, options, load.Load)
	if code != 1 || stdout.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	for _, expected := range []string{
		"canonical context.Context identity could not be established safely",
		"1 lifecycle hook error(s)",
	} {
		if !strings.Contains(stderr.String(), expected) {
			t.Fatalf("stderr=%q missing %q", stderr.String(), expected)
		}
	}
}

func cliGoRoot(t *testing.T) string {
	t.Helper()
	output, err := exec.CommandContext(t.Context(), "go", "env", "GOROOT").Output()
	if err != nil {
		t.Fatalf("go env GOROOT: %v", err)
	}
	return strings.TrimSpace(string(output))
}
