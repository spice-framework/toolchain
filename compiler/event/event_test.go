package event

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/spice-framework/toolchain/compiler/load"
	"github.com/spice-framework/toolchain/compiler/modulith"
	"github.com/spice-framework/toolchain/compiler/provider"
	"github.com/spice-framework/toolchain/compiler/resolve"
	"github.com/spice-framework/toolchain/internal/testannotation"
	"github.com/spice-framework/toolchain/internal/testsupport"
)

func TestBuildCompilesTypedTopicsAndListeners(t *testing.T) {
	root := writeEventModule(t, map[string]string{
		"orders/events.go": `package orders

import (
	"context"

	"github.com/spice-framework/spice/event"
)

type OrderPlaced struct {
	ID string
}

type Audit struct{}
type Inventory struct{}

// @Bean
func NewAudit() *Audit {
	panic("provider bodies must not execute during analysis")
}

// @Bean
func NewInventory() *Inventory {
	panic("provider bodies must not execute during analysis")
}

// @event.Listener(order=20)
func (*Audit) Record(context.Context, OrderPlaced) error {
	panic("listener methods must not execute during analysis")
}

// @event.Listener(order=-10)
func (*Inventory) Reserve(context.Context, OrderPlaced) error {
	panic("listener methods must not execute during analysis")
}

// @event.Topic
func OrderEvents(audit *Audit, inventory *Inventory) event.Publisher[OrderPlaced] {
	panic("event topic marker bodies must not execute during analysis")
}
`,
	})
	program, resolution, providers, modules := loadEventInputs(t, root)
	catalog := Build(program, resolution, providers, modules)
	if diagnostics := catalog.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("Diagnostics() = %v", diagnosticStrings(diagnostics))
	}
	topics := catalog.Topics()
	if len(topics) != 1 {
		t.Fatalf("Topics() = %#v", topics)
	}
	topic := topics[0]
	if topic.Name != "OrderEvents" ||
		topic.ProviderID == "" ||
		topic.Module != "example.com/events/orders" ||
		topic.PublisherTypeID !=
			"github.com/spice-framework/spice/event.Publisher[example.com/events/orders.OrderPlaced]" ||
		topic.PayloadTypeID != "example.com/events/orders.OrderPlaced" {
		t.Fatalf("Topic = %#v", topic)
	}
	listeners := topic.Listeners()
	if len(listeners) != 2 ||
		listeners[0].Method.Name != "Reserve" ||
		listeners[0].Order != -10 ||
		listeners[1].Method.Name != "Record" ||
		listeners[1].Order != 20 {
		t.Fatalf("Listeners() = %#v", listeners)
	}
	synthetic := catalog.Providers()
	if len(synthetic) != 1 ||
		synthetic[0].Source != provider.SourceEvent ||
		!synthetic[0].ReturnsError ||
		len(synthetic[0].Dependencies) != 2 ||
		synthetic[0].Dependencies[0].Name != "audit" ||
		synthetic[0].Dependencies[1].Name != "inventory" {
		t.Fatalf("Providers() = %#v", synthetic)
	}

	topics[0].Name = "changed"
	listeners[0].Order = 999
	synthetic[0].Dependencies[0].Name = "changed"
	if catalog.Topics()[0].Name == "changed" ||
		catalog.Topics()[0].Listeners()[0].Order == 999 ||
		catalog.Providers()[0].Dependencies[0].Name == "changed" {
		t.Fatal("catalog accessors returned mutable internal storage")
	}
}

func TestBuildRejectsInvalidEventContracts(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "listener target",
			source: `package app

import "context"

// @event.Listener
func Listen(context.Context, Event) error { return nil }

type Event struct{}
`,
			want: "event listeners must target ordinary methods",
		},
		{
			name: "listener signature",
			source: `package app

import "context"

type Event struct{}
type Listener struct{}

// @Bean
func NewListener() Listener { return Listener{} }

// @event.Listener
func (Listener) Handle(context.Context, Event) {}
`,
			want: "require exact signature",
		},
		{
			name: "listener provider",
			source: `package app

import "context"

type Event struct{}
type Listener struct{}

// @event.Listener
func (Listener) Handle(context.Context, Event) error { return nil }
`,
			want: "requires exactly one exact @Bean provider",
		},
		{
			name: "listener arguments",
			source: `package app

import "context"

type Event struct{}
type Listener struct{}

// @Bean
func NewListener() Listener { return Listener{} }

// @event.Listener(order="first")
func (Listener) Handle(context.Context, Event) error { return nil }
`,
			want: `argument "order" requires integer`,
		},
		{
			name: "topic target",
			source: `package app

import "github.com/spice-framework/spice/event"

type Event struct{}
type Marker struct{}

// @event.Topic
func (Marker) Topic() event.Publisher[Event] { return nil }
`,
			want: "event topics must target ordinary package-level functions",
		},
		{
			name: "topic marker",
			source: `package app

import "github.com/spice-framework/spice/event"

type Event struct{}

// @event.Topic
func topic() event.Publisher[Event] { return nil }
`,
			want: "event topic markers must be exported",
		},
		{
			name: "topic result",
			source: `package app

// @event.Topic
func Topic() string { return "" }
`,
			want: "must return exact event.Publisher",
		},
		{
			name: "pointer payload",
			source: `package app

import "github.com/spice-framework/spice/event"

type Event struct{}

// @event.Topic
func Topic() event.Publisher[*Event] { return nil }
`,
			want: "for an exported named event value",
		},
		{
			name: "missing topic listener",
			source: `package app

import "github.com/spice-framework/spice/event"

type Event struct{}
type Listener struct{}

// @Bean
func NewListener() Listener { return Listener{} }

// @event.Topic
func Topic(Listener) event.Publisher[Event] { return nil }
`,
			want: "requires exactly one @event.Listener method",
		},
		{
			name: "unassigned listener",
			source: `package app

import "context"

type Event struct{}
type Listener struct{}

// @Bean
func NewListener() Listener { return Listener{} }

// @event.Listener
func (Listener) Handle(context.Context, Event) error { return nil }
`,
			want: "is not selected by any @event.Topic marker",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := writeEventModule(t, map[string]string{
				"app/contracts.go": test.source,
			})
			program, resolution, providers, modules := loadEventInputs(
				t,
				root,
			)
			diagnostics := Build(
				program,
				resolution,
				providers,
				modules,
			).Diagnostics()
			if len(diagnostics) == 0 ||
				!strings.Contains(
					strings.Join(diagnosticStrings(diagnostics), "\n"),
					test.want,
				) {
				t.Fatalf(
					"Diagnostics() = %v, want containing %q",
					diagnosticStrings(diagnostics),
					test.want,
				)
			}
		})
	}
}

func TestBuildDiagnosticsAreDeterministic(t *testing.T) {
	root := writeEventModule(t, map[string]string{
		"app/contracts.go": `package app

import (
	"context"

	"github.com/spice-framework/spice/event"
)

type Event struct{}
type Listener struct{}

// @Bean
func NewListener() Listener { return Listener{} }

// @event.Listener
// @event.Listener
func (Listener) Handle(context.Context, Event) error { return nil }

// @event.Topic
// @event.Topic
func Topic(Listener) event.Publisher[Event] { return nil }
`,
	})
	program, resolution, providers, modules := loadEventInputs(t, root)
	var baseline []string
	for run := range 4 {
		if run%2 == 1 {
			slices.Reverse(resolution.Occurrences)
		}
		got := diagnosticStrings(
			Build(program, resolution, providers, modules).Diagnostics(),
		)
		if run == 0 {
			baseline = got
		} else if !slices.Equal(got, baseline) {
			t.Fatalf(
				"run %d diagnostics changed:\nfirst=%v\nnext=%v",
				run,
				baseline,
				got,
			)
		}
	}
	joined := strings.Join(baseline, "\n")
	for _, expected := range []string{
		"carries @event.Listener more than once",
		"carries @event.Topic more than once",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("diagnostics missing %q:\n%s", expected, joined)
		}
	}
}

func TestBuildSupportsExactAliasesAndModuleOwnership(t *testing.T) {
	root := writeEventModule(t, map[string]string{
		"orders/doc.go": `// Package orders owns order events.
//
// @Module
package orders
`,
		"orders/events.go": `package orders

import (
	"context"

	"github.com/spice-framework/spice/event"
)

type ContextAlias = context.Context
type ErrorAlias = error
type Event struct{}
type EventAlias = Event
type Listener struct{}
type ListenerAlias = *Listener

// @Bean
func NewListener() *Listener { return &Listener{} }

// @event.Listener
func (*Listener) Handle(ContextAlias, EventAlias) ErrorAlias { return nil }

// @event.Topic
func Events(ListenerAlias) event.Publisher[EventAlias] { return nil }
`,
	})
	program, resolution, providers, modules := loadEventInputs(t, root)
	catalog := Build(program, resolution, providers, modules)
	if diagnostics := catalog.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("Diagnostics() = %v", diagnosticStrings(diagnostics))
	}
	topic := catalog.Topics()[0]
	if topic.Module != "example.com/events/orders" ||
		topic.Listeners()[0].Module != "example.com/events/orders" {
		t.Fatalf("module ownership = topic %#v listeners %#v", topic, topic.Listeners())
	}
}

func TestBuildNilProgram(t *testing.T) {
	diagnostics := Build(
		nil,
		resolve.Result{},
		provider.Catalog{},
		modulith.Model{},
	).Diagnostics()
	if len(diagnostics) != 1 ||
		diagnostics[0].Kind != "internal" ||
		!strings.Contains(
			diagnostics[0].Error(),
			"event catalog requires a loaded program",
		) {
		t.Fatalf("Build(nil) diagnostics = %#v", diagnostics)
	}
}

func loadEventInputs(
	t *testing.T,
	root string,
) (
	*load.Program,
	resolve.Result,
	provider.Catalog,
	modulith.Model,
) {
	t.Helper()
	program, err := load.Load(
		context.Background(),
		load.Options{Dir: root},
		"./...",
	)
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
	modules := modulith.Build(program, resolution)
	if diagnostics := modules.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("module diagnostics = %v", diagnostics)
	}
	return program, resolution, providers, modules
}

func writeEventModule(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	repository := repositoryRoot(t)
	allFiles := map[string]string{
		"go.mod": "module example.com/events\n\ngo 1.26.0\n\n" +
			"require github.com/spice-framework/spice v0.0.0\n\n" +
			"replace github.com/spice-framework/spice => " +
			filepath.ToSlash(repository) + "\n",
	}
	maps.Copy(allFiles, files)
	paths := make([]string, 0, len(allFiles))
	for name := range allFiles {
		paths = append(paths, name)
	}
	sort.Strings(paths)
	for _, name := range paths {
		fullPath := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(
			fullPath,
			[]byte(allFiles[name]),
			0o600,
		); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	return testsupport.CoreDirectory(t)
}

func diagnosticStrings(diagnostics []Diagnostic) []string {
	result := make([]string, len(diagnostics))
	for index, diagnostic := range diagnostics {
		result[index] = diagnostic.Error()
	}
	return result
}
