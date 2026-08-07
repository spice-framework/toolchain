package application

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spice-framework/toolchain/compiler/provider"
)

func TestBuildAddsGeneratedEventPublisherToApplicationGraph(t *testing.T) {
	root := writeEventApplicationModule(t, `package app

import (
	"context"

	"github.com/spice-framework/spice/event"
)

// @event.Topic
type OrderPlaced struct {
	ID string
}

type Listener struct{}

// @Bean
func NewListener() *Listener { return &Listener{} }

// @event.Listener(order=5)
func (*Listener) Handle(context.Context, OrderPlaced) error { return nil }

type Service struct {
	Publisher event.Publisher[OrderPlaced]
}

// @Bean
func NewService(publisher event.Publisher[OrderPlaced]) *Service {
	return &Service{Publisher: publisher}
}

// @Application
func Application(*Service) {}
`)
	program, resolution := loadAndResolve(t, root, "./...")
	model := Build(program, resolution)
	if diagnostics := model.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("Build() diagnostics = %v", diagnosticStrings(diagnostics))
	}
	if got, want := providerNames(model.Providers()), []string{
		"NewListener",
		"OrderPlaced",
		"NewService",
	}; !slices.Equal(got, want) {
		t.Fatalf("provider order = %v, want %v", got, want)
	}
	providers := model.Providers()
	if providers[1].Source != provider.SourceEvent ||
		providers[1].OutputTypeID !=
			"github.com/spice-framework/spice/event.Publisher[example.com/eventapplication/app.OrderPlaced]" {
		t.Fatalf("event provider = %#v", providers[1])
	}
	events := model.Events()
	if len(events) != 1 ||
		events[0].Name != "OrderPlaced" ||
		len(events[0].Listeners()) != 1 ||
		events[0].Listeners()[0].Method.Name != "Handle" {
		t.Fatalf("Events() = %#v", events)
	}
	edges := model.Edges()
	if len(edges) != 2 ||
		edges[0].ConsumerID != providers[2].SymbolID ||
		edges[0].DependencyID != providers[1].SymbolID ||
		edges[1].ConsumerID != providers[1].SymbolID ||
		edges[1].DependencyID != providers[0].SymbolID {
		t.Fatalf("Edges() = %#v", edges)
	}

	events[0].Name = "changed"
	listeners := events[0].Listeners()
	listeners[0].Order = 99
	if model.Events()[0].Name == "changed" ||
		model.Events()[0].Listeners()[0].Order == 99 {
		t.Fatal("Events() returned mutable model storage")
	}
}

func TestBuildReportsGeneratedEventDependencyCycle(t *testing.T) {
	root := writeEventApplicationModule(t, `package app

import (
	"context"

	"github.com/spice-framework/spice/event"
)

// @event.Topic
type OrderPlaced struct{}
type Listener struct{}

// @Bean
func NewListener(event.Publisher[OrderPlaced]) *Listener {
	return &Listener{}
}

// @event.Listener
func (*Listener) Handle(context.Context, OrderPlaced) error { return nil }

// @Application
func Application(*Listener) {}
`)
	program, resolution := loadAndResolve(t, root, "./...")
	diagnostics := Build(program, resolution).Diagnostics()
	if len(diagnostics) != 1 ||
		diagnostics[0].Stage != StageGraph ||
		!strings.Contains(diagnostics[0].Message, "provider dependency cycle") {
		t.Fatalf("Build() diagnostics = %#v", diagnostics)
	}
}

func TestBuildRejectsDuplicateGeneratedPublisherTypes(t *testing.T) {
	root := writeEventApplicationModule(t, `package app

import (
	"context"

	"github.com/spice-framework/spice/event"
)

type OrderPlaced struct{}
type Audit struct{}
type Inventory struct{}

// @Bean
func NewAudit() *Audit { return &Audit{} }

// @Bean
func NewInventory() *Inventory { return &Inventory{} }

// @event.Listener
func (*Audit) Handle(context.Context, OrderPlaced) error { return nil }

// @event.Listener
func (*Inventory) Handle(context.Context, OrderPlaced) error { return nil }

// @event.Topic
func AuditEvents(*Audit) event.Publisher[OrderPlaced] { return nil }

// @event.Topic
func InventoryEvents(*Inventory) event.Publisher[OrderPlaced] { return nil }
`)
	program, resolution := loadAndResolve(t, root, "./...")
	diagnostics := Build(program, resolution).Diagnostics()
	if len(diagnostics) != 1 ||
		diagnostics[0].Stage != StageProvider ||
		!strings.Contains(
			diagnostics[0].Message,
			"multiple providers produce exact type",
		) {
		t.Fatalf("Build() diagnostics = %#v", diagnostics)
	}
}

func writeEventApplicationModule(t *testing.T, source string) string {
	t.Helper()
	return writeModule(t, map[string]string{
		"go.mod": "module example.com/eventapplication\n\ngo 1.26.0\n\n" +
			"require github.com/spice-framework/spice v0.0.0\n\n" +
			"replace github.com/spice-framework/spice => " +
			filepath.ToSlash(applicationRepositoryRoot(t)) + "\n",
		"app/application.go": source,
	})
}
