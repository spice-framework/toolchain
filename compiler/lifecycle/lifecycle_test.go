package lifecycle

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/StevenBuglione/spice/compiler/load"
	"github.com/StevenBuglione/spice/compiler/provider"
	"github.com/StevenBuglione/spice/compiler/resolve"
	"github.com/StevenBuglione/spice/internal/testannotation"
)

func TestCatalogAcceptsLifecycleHooks(t *testing.T) {
	root := writeModule(t, map[string]string{
		"go.mod": "module example.com/hooks\n\ngo 1.23.0\n",
		"app/hooks.go": `package app
import "context"
type Pointer struct{}
type PointerAlias = *Pointer
type Value struct{}
type Passive struct{}
type ContextAlias = context.Context
type ErrorAlias = error
// @Bean
func PointerProvider() PointerAlias { panic("provider must not execute") }
// @OnStart
func (*Pointer) Boot(ContextAlias) ErrorAlias { panic("hook must not execute") }
// @OnStop
func (*Pointer) Halt(context.Context) error { panic("hook must not execute") }
// @Bean
func ValueProvider() Value { panic("provider must not execute") }
// @OnStart
func (Value) Engage(context.Context) error { panic("hook must not execute") }
// @Bean
func PassiveProvider() Passive { panic("provider must not execute") }
`,
	})
	program, resolution, providers := loadCatalogs(t, root)
	catalog, stats := build(program, resolution, providers)
	if diagnostics := catalog.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("Diagnostics() = %v", diagnosticStrings(diagnostics))
	}
	components := catalog.Components()
	if len(components) != 2 {
		t.Fatalf("Components() = %#v, want two participating providers", components)
	}
	if components[0].Provider.SymbolID >= components[1].Provider.SymbolID {
		t.Fatalf("components not sorted by provider ID: %#v", components)
	}
	pointer := componentByProvider(components, "PointerProvider")
	if pointer == nil || pointer.Start == nil || pointer.Stop == nil {
		t.Fatalf("PointerProvider component = %#v", pointer)
	}
	if pointer.Start.Kind != Start || pointer.Stop.Kind != Stop || pointer.Start.ProviderID != pointer.Provider.SymbolID {
		t.Fatalf("pointer hooks = start %#v stop %#v", pointer.Start, pointer.Stop)
	}
	if pointer.Start.ReceiverTypeID != "*example.com/hooks/app.Pointer" {
		t.Fatalf("pointer receiver ID = %q", pointer.Start.ReceiverTypeID)
	}
	value := componentByProvider(components, "ValueProvider")
	if value == nil || value.Start == nil || value.Stop != nil {
		t.Fatalf("ValueProvider component = %#v", value)
	}
	if stats.identityChecks != 3 {
		t.Fatalf("identity checks = %d, want one bounded check per hook", stats.identityChecks)
	}
	copyOne := catalog.Components()
	copyOne[0].Start.Kind = Stop
	if catalog.Components()[0].Start.Kind != Start {
		t.Fatal("Components() did not return defensive hook copies")
	}
}

func TestCatalogRejectsInvalidLifecycleHooks(t *testing.T) {
	var source strings.Builder
	source.WriteString("package app\n\nimport (\"context\"; \"time\")\n\ntype ContextLike interface { Deadline() (time.Time, bool); Done() <-chan struct{}; Err() error; Value(any) any }\ntype ErrorLike interface { Error() string }\n")
	for _, snippet := range []string{
		"type StopOnly struct{}\n// @Bean\nfunc StopOnlyProvider() StopOnly { panic(\"must not execute\") }\n// @OnStop\nfunc (StopOnly) Halt(context.Context) error { panic(\"must not execute\") }\n",
		"type DuplicateStart struct{}\n// @Bean\nfunc DuplicateStartProvider() DuplicateStart { panic(\"must not execute\") }\n// @OnStart\nfunc (DuplicateStart) BootOne(context.Context) error { panic(\"must not execute\") }\n// @OnStart\nfunc (DuplicateStart) BootTwo(context.Context) error { panic(\"must not execute\") }\n",
		"type DuplicateStop struct{}\n// @Bean\nfunc DuplicateStopProvider() DuplicateStop { panic(\"must not execute\") }\n// @OnStart\nfunc (DuplicateStop) Boot(context.Context) error { panic(\"must not execute\") }\n// @OnStop\nfunc (DuplicateStop) HaltOne(context.Context) error { panic(\"must not execute\") }\n// @OnStop\nfunc (DuplicateStop) HaltTwo(context.Context) error { panic(\"must not execute\") }\n",
		"type Both struct{}\n// @Bean\nfunc BothProvider() Both { panic(\"must not execute\") }\n// @OnStart\n// @OnStop\nfunc (Both) Boot(context.Context) error { panic(\"must not execute\") }\n",
		"type Repeated struct{}\n// @Bean\nfunc RepeatedProvider() Repeated { panic(\"must not execute\") }\n// @OnStart\n// @OnStart\nfunc (Repeated) Boot(context.Context) error { panic(\"must not execute\") }\n",
		"type ValueToPointer struct{}\n// @Bean\nfunc ValueToPointerProvider() ValueToPointer { panic(\"must not execute\") }\n// @OnStart\nfunc (*ValueToPointer) Boot(context.Context) error { panic(\"must not execute\") }\n",
		"type PointerToValue struct{}\n// @Bean\nfunc PointerToValueProvider() *PointerToValue { panic(\"must not execute\") }\n// @OnStart\nfunc (PointerToValue) Boot(context.Context) error { panic(\"must not execute\") }\n",
		"type Orphan struct{}\n// @OnStart\nfunc (Orphan) Boot(context.Context) error { panic(\"must not execute\") }\n",
		"type Underlying int\n// @Bean\nfunc UnderlyingProvider() int { panic(\"must not execute\") }\n// @OnStart\nfunc (Underlying) Boot(context.Context) error { panic(\"must not execute\") }\n",
		"type ZeroParam struct{}\n// @Bean\nfunc ZeroParamProvider() ZeroParam { panic(\"must not execute\") }\n// @OnStart\nfunc (ZeroParam) Boot() error { panic(\"must not execute\") }\n",
		"type TwoParam struct{}\n// @Bean\nfunc TwoParamProvider() TwoParam { panic(\"must not execute\") }\n// @OnStart\nfunc (TwoParam) Boot(context.Context, int) error { panic(\"must not execute\") }\n",
		"type WrongContext struct{}\n// @Bean\nfunc WrongContextProvider() WrongContext { panic(\"must not execute\") }\n// @OnStart\nfunc (WrongContext) Boot(ContextLike) error { panic(\"must not execute\") }\n",
		"type UnnamedContext struct{}\n// @Bean\nfunc UnnamedContextProvider() UnnamedContext { panic(\"must not execute\") }\n// @OnStart\nfunc (UnnamedContext) Boot(interface{ Deadline() (time.Time, bool); Done() <-chan struct{}; Err() error; Value(any) any }) error { panic(\"must not execute\") }\n",
		"type EmbeddedContext struct{}\n// @Bean\nfunc EmbeddedContextProvider() EmbeddedContext { panic(\"must not execute\") }\n// @OnStart\nfunc (EmbeddedContext) Boot(interface{ context.Context }) error { panic(\"must not execute\") }\n",
		"type ZeroResult struct{}\n// @Bean\nfunc ZeroResultProvider() ZeroResult { panic(\"must not execute\") }\n// @OnStart\nfunc (ZeroResult) Boot(context.Context) { panic(\"must not execute\") }\n",
		"type TwoResult struct{}\n// @Bean\nfunc TwoResultProvider() TwoResult { panic(\"must not execute\") }\n// @OnStart\nfunc (TwoResult) Boot(context.Context) (error, error) { panic(\"must not execute\") }\n",
		"type BadResult struct{}\n// @Bean\nfunc BadResultProvider() BadResult { panic(\"must not execute\") }\n// @OnStart\nfunc (BadResult) Boot(context.Context) ErrorLike { panic(\"must not execute\") }\n",
		"type Variadic struct{}\n// @Bean\nfunc VariadicProvider() Variadic { panic(\"must not execute\") }\n// @OnStart\nfunc (Variadic) Boot(...context.Context) error { panic(\"must not execute\") }\n",
		"type Box[T any] struct{}\n// @Bean\nfunc BoxProvider() Box[int] { panic(\"must not execute\") }\n// @OnStart\nfunc (Box[T]) Boot(context.Context) error { panic(\"must not execute\") }\n",
	} {
		source.WriteString(snippet)
	}
	root := writeModule(t, map[string]string{"go.mod": "module example.com/invalidhooks\n\ngo 1.23.0\n", "app/hooks.go": source.String()})
	program, resolution, providers := loadCatalogs(t, root)
	var first string
	for run := range 5 {
		if run%2 == 1 {
			for left, right := 0, len(resolution.Occurrences)-1; left < right; left, right = left+1, right-1 {
				resolution.Occurrences[left], resolution.Occurrences[right] = resolution.Occurrences[right], resolution.Occurrences[left]
			}
		}
		catalog := Build(program, resolution, providers)
		joined := strings.Join(diagnosticStrings(catalog.Diagnostics()), "\n")
		if len(catalog.Diagnostics()) != 19 {
			t.Fatalf("run %d diagnostic count = %d, want 19:\n%s", run, len(catalog.Diagnostics()), joined)
		}
		if run == 0 {
			first = joined
		} else if joined != first {
			t.Fatalf("run %d diagnostics changed:\nfirst=%s\nnext=%s", run, first, joined)
		}
	}
	for _, expected := range []string{
		"no corresponding @OnStart", "multiple @OnStart hooks", "multiple @OnStop hooks", "cannot be combined", "not repeatable",
		"exact receiver type *example.com/invalidhooks/app.ValueToPointer", "exact receiver type example.com/invalidhooks/app.PointerToValue",
		"exact receiver type example.com/invalidhooks/app.Orphan", "exact receiver type example.com/invalidhooks/app.Underlying",
		"exactly one explicit context parameter, got 0", "exactly one explicit context parameter, got 2", "exact loaded context.Context type",
		"exactly one error result, got 0", "exactly one error result, got 2", "exact predeclared error type",
		"non-variadic", "receiver-generic lifecycle methods are not supported",
	} {
		if !strings.Contains(first, expected) {
			t.Fatalf("diagnostics missing %q:\n%s", expected, first)
		}
	}
}

func TestCatalogLifecycleHooksDeterministic(t *testing.T) {
	files := map[string]string{
		"go.mod": "module example.com/deterministic-hooks\n\ngo 1.23.0\n",
		"app/a.go": `package app
import "context"
type A struct{}
// @Bean
func AProvider() A { panic("must not execute") }
// @OnStart
func (A) Begin(context.Context) error { panic("must not execute") }
`,
		"app/z.go": `package app
import "context"
type Z struct{}
// @Bean
func ZProvider() Z { panic("must not execute") }
// @OnStart
func (Z) Begin(context.Context) error { panic("must not execute") }
// @OnStop
func (Z) End(context.Context) error { panic("must not execute") }
`,
	}
	var first string
	for run := range 5 {
		program, resolution, providers := loadCatalogs(t, writeModule(t, files))
		if run%2 == 1 {
			for left, right := 0, len(resolution.Occurrences)-1; left < right; left, right = left+1, right-1 {
				resolution.Occurrences[left], resolution.Occurrences[right] = resolution.Occurrences[right], resolution.Occurrences[left]
			}
		}
		catalog := Build(program, resolution, providers)
		if diagnostics := catalog.Diagnostics(); len(diagnostics) != 0 {
			t.Fatalf("run %d diagnostics = %v", run, diagnosticStrings(diagnostics))
		}
		summary := catalogSummary(catalog)
		if run == 0 {
			first = summary
		} else if summary != first {
			t.Fatalf("run %d summary changed:\nfirst=%s\nnext=%s", run, first, summary)
		}
	}
}

func TestCatalogLifecycleHooksUsesBoundedIdentityChecks(t *testing.T) {
	const count = 192
	var source strings.Builder
	source.WriteString("package app\n\nimport \"context\"\n\n")
	for index := range count {
		fmt.Fprintf(&source, "type T%03d struct{}\n// @Bean\nfunc P%03d() T%03d { panic(\"must not execute\") }\n// @OnStart\nfunc (T%03d) H%03d(context.Context) error { panic(\"must not execute\") }\n", index, index, index, index, index)
	}
	root := writeModule(t, map[string]string{
		"go.mod":       "module example.com/bounded-hooks\n\ngo 1.23.0\n",
		"app/hooks.go": source.String(),
	})
	program, resolution, providers := loadCatalogs(t, root)
	catalog, stats := build(program, resolution, providers)
	if diagnostics := catalog.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("Diagnostics() = %v", diagnosticStrings(diagnostics))
	}
	if len(catalog.Components()) != count || stats.identityChecks > count*2 {
		t.Fatalf("components=%d identityChecks=%d, want %d components and bounded indexed checks", len(catalog.Components()), stats.identityChecks, count)
	}
}

func loadCatalogs(t *testing.T, root string) (*load.Program, resolve.Result, provider.Catalog) {
	t.Helper()
	program, err := load.Load(context.Background(), load.Options{Dir: root}, "./...")
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
	return program, resolution, providers
}

func writeModule(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(files[path]), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func componentByProvider(components []Component, name string) *Component {
	for index := range components {
		if components[index].Provider.Name == name {
			return &components[index]
		}
	}
	return nil
}

func diagnosticStrings(diagnostics []Diagnostic) []string {
	result := make([]string, len(diagnostics))
	for index, diagnostic := range diagnostics {
		result[index] = diagnostic.Error()
	}
	return result
}

func catalogSummary(catalog Catalog) string {
	var summary strings.Builder
	for _, component := range catalog.Components() {
		fmt.Fprintf(&summary, "%s", component.Provider.SymbolID)
		if component.Start != nil {
			fmt.Fprintf(&summary, "|start:%s:%s", component.Start.MethodID, component.Start.ReceiverTypeID)
		}
		if component.Stop != nil {
			fmt.Fprintf(&summary, "|stop:%s:%s", component.Stop.MethodID, component.Stop.ReceiverTypeID)
		}
		summary.WriteByte('\n')
	}
	for _, diagnostic := range catalog.Diagnostics() {
		fmt.Fprintf(&summary, "%s|%s|%s\n", diagnostic.Kind, diagnostic.MethodID, diagnostic.Message)
	}
	return summary.String()
}
