package lifecycle

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spice-framework/toolchain/compiler/load"
	"github.com/spice-framework/toolchain/compiler/provider"
	"github.com/spice-framework/toolchain/compiler/resolve"
	"github.com/spice-framework/toolchain/internal/testannotation"
)

func TestCatalogRejectsWrongShapeCanonicalContextOverlay(t *testing.T) {
	contextSource := filepath.Join(goRoot(t), "src", "context", "context.go")
	tests := []struct {
		name   string
		source string
	}{
		{name: "wrong kind", source: "package context\n\ntype Context string\n"},
		{
			name: "wrong method signature",
			source: `package context

import "time"

type Context interface {
	Deadline() (time.Time, bool)
	Done() <-chan struct{}
	Err() error
	Value(string) any
}
`,
		},
		{
			name: "named empty Done element",
			source: `package context

import "time"

type doneToken struct{}

type Context interface {
	Deadline() (time.Time, bool)
	Done() <-chan doneToken
	Err() error
	Value(any) any
}
`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := writeModule(t, map[string]string{
				"go.mod": "module example.com/context-overlay\n\ngo 1.23.0\n",
				"app/app.go": `package app
import "context"
type Server struct{}
// @Bean
func ServerProvider() Server { panic("provider must not execute") }
// @OnStart
func (Server) Boot(context.Context) error { panic("hook must not execute") }
`,
			})
			program, err := load.Load(context.Background(), load.Options{
				Dir:     root,
				Overlay: map[string][]byte{contextSource: []byte(test.source)},
			}, "./...")
			if err != nil {
				t.Fatalf("load.Load() error = %v", err)
			}
			resolution := resolve.Annotations(program)
			if len(resolution.Diagnostics) != 0 {
				t.Fatalf("resolve diagnostics = %v", resolution.Diagnostics)
			}
			resolution, err = testannotation.AttachOfficial(resolution)
			if err != nil {
				t.Fatalf("AttachOfficial() error = %v", err)
			}
			providers := provider.Build(program, resolution)
			if diagnostics := providers.Diagnostics(); len(diagnostics) != 0 {
				t.Fatalf("provider diagnostics = %v", diagnostics)
			}
			diagnostics := Build(program, resolution, providers).Diagnostics()
			if len(diagnostics) != 1 || !strings.Contains(diagnostics[0].Message, "canonical context.Context identity could not be established safely") {
				t.Fatalf("Diagnostics() = %v, want one fail-closed canonical context diagnostic", diagnosticStrings(diagnostics))
			}
		})
	}
}

func TestCatalogAcceptsExactCanonicalContextDoneAliasOverlay(t *testing.T) {
	root := writeModule(t, map[string]string{
		"go.mod": "module example.com/context-overlay\n\ngo 1.23.0\n",
		"app/app.go": `package app
import "context"
type Server struct{}
// @Bean
func ServerProvider() Server { panic("provider must not execute") }
// @OnStart
func (Server) Boot(context.Context) error { panic("hook must not execute") }
`,
	})
	contextSource := filepath.Join(goRoot(t), "src", "context", "context.go")
	program, err := load.Load(context.Background(), load.Options{
		Dir: root,
		Overlay: map[string][]byte{contextSource: []byte(`package context

import "time"

type doneToken = struct{}

type Context interface {
	Deadline() (time.Time, bool)
	Done() <-chan doneToken
	Err() error
	Value(any) any
}
`)},
	}, "./...")
	if err != nil {
		t.Fatalf("load.Load() error = %v", err)
	}
	resolution := resolve.Annotations(program)
	if len(resolution.Diagnostics) != 0 {
		t.Fatalf("resolve diagnostics = %v", resolution.Diagnostics)
	}
	resolution, err = testannotation.AttachOfficial(resolution)
	if err != nil {
		t.Fatalf("AttachOfficial() error = %v", err)
	}
	providers := provider.Build(program, resolution)
	if diagnostics := providers.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("provider diagnostics = %v", diagnostics)
	}
	if diagnostics := Build(program, resolution, providers).Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("Diagnostics() = %v, want exact alias-backed Done element accepted", diagnosticStrings(diagnostics))
	}
}

func goRoot(t *testing.T) string {
	t.Helper()
	output, err := exec.CommandContext(t.Context(), "go", "env", "GOROOT").Output()
	if err != nil {
		t.Fatalf("go env GOROOT: %v", err)
	}
	return strings.TrimSpace(string(output))
}
