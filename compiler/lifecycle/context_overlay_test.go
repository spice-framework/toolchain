package lifecycle

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/StevenBuglione/spice/compiler/load"
	"github.com/StevenBuglione/spice/compiler/provider"
	"github.com/StevenBuglione/spice/compiler/resolve"
)

func TestCatalogRejectsWrongShapeCanonicalContextOverlay(t *testing.T) {
	contextSource := filepath.Join(runtime.GOROOT(), "src", "context", "context.go")
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
	}
	for _, test := range tests {
		test := test
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
