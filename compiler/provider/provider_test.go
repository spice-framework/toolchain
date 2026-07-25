package provider

import (
	"context"
	"fmt"
	"go/types"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/StevenBuglione/spice/compiler/load"
	"github.com/StevenBuglione/spice/compiler/resolve"
)

func TestCatalogValidProviders(t *testing.T) {
	root := writeModule(t, map[string]string{
		"go.mod": "module example.com/providers\n\ngo 1.23.0\n",
		"api/api.go": `package api

type Logger interface { Log(string) }
`,
		"app/providers.go": `package app

import "example.com/providers/api"

type Config struct{}
type Store struct{}
type Service struct{}
type NamedA int
type NamedB int

// @Bean
func ConfigProvider() Config { panic("provider body must not execute") }

// @Bean
func StoreProvider(config Config) *Store { panic("provider body must not execute") }

// @Bean
func ServiceProvider(config Config, logger api.Logger) (*Service, error) {
	panic("provider body must not execute")
}

// @Bean
func StringsProvider() []string { panic("provider body must not execute") }

// @Bean
func MapProvider() map[string]int { panic("provider body must not execute") }

// @Bean
func NamedAProvider() NamedA { panic("provider body must not execute") }

// @Bean
func NamedBProvider() NamedB { panic("provider body must not execute") }
`,
	})

	program, resolved := loadAndResolve(t, root, "./...")
	catalog := buildQuiet(t, program, resolved)
	if diagnostics := catalog.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("Build() diagnostics = %v", diagnosticStrings(diagnostics))
	}
	providers := catalog.Providers()
	if len(providers) != 7 {
		t.Fatalf("len(Providers()) = %d, want 7", len(providers))
	}

	config := providerByName(providers, "ConfigProvider")
	if config == nil || config.ReturnsError || len(config.Dependencies) != 0 {
		t.Fatalf("ConfigProvider = %#v", config)
	}
	if config.OutputTypeID != "example.com/providers/app.Config" {
		t.Fatalf("ConfigProvider output ID = %q", config.OutputTypeID)
	}

	store := providerByName(providers, "StoreProvider")
	if store == nil || store.OutputTypeID != "*example.com/providers/app.Store" || len(store.Dependencies) != 1 {
		t.Fatalf("StoreProvider = %#v", store)
	}
	if store.Dependencies[0].TypeID != "example.com/providers/app.Config" || store.Dependencies[0].Index != 0 || store.Dependencies[0].Name != "config" {
		t.Fatalf("StoreProvider dependency = %#v", store.Dependencies[0])
	}

	service := providerByName(providers, "ServiceProvider")
	if service == nil || !service.ReturnsError || len(service.Dependencies) != 2 {
		t.Fatalf("ServiceProvider = %#v", service)
	}
	if service.Dependencies[1].TypeID != "example.com/providers/api.Logger" || service.Dependencies[1].Name != "logger" {
		t.Fatalf("ServiceProvider imported dependency = %#v", service.Dependencies[1])
	}
	if !types.Identical(service.Output, service.Symbol.Signature.Results().At(0).Type()) ||
		!types.Identical(service.Dependencies[0].Type, service.Symbol.Signature.Params().At(0).Type()) {
		t.Fatal("catalog did not retain live types from the loaded program")
	}

	wantTypes := map[string]string{
		"StringsProvider": "[]string",
		"MapProvider":     "map[string]int",
		"NamedAProvider":  "example.com/providers/app.NamedA",
		"NamedBProvider":  "example.com/providers/app.NamedB",
	}
	for name, want := range wantTypes {
		provider := providerByName(providers, name)
		if provider == nil || provider.OutputTypeID != want {
			t.Fatalf("%s output ID = %#v, want %q", name, provider, want)
		}
		if strings.Contains(provider.OutputTypeID, filepath.ToSlash(root)) {
			t.Fatalf("%s output ID leaked fixture path: %q", name, provider.OutputTypeID)
		}
	}

	copyOne := catalog.Providers()
	copyOne[0].Dependencies = append(copyOne[0].Dependencies, Dependency{TypeID: "mutated"})
	if reflect.DeepEqual(copyOne, catalog.Providers()) {
		t.Fatal("Providers() did not return a defensive copy")
	}
}

func TestCatalogRejectsUnsupportedSignatures(t *testing.T) {
	root := writeModule(t, map[string]string{
		"go.mod": "module example.com/invalidproviders\n\ngo 1.23.0\n",
		"providers.go": `package invalidproviders

type A struct{}
type B struct{}

// @Bean
func NoResult() {}

// @Bean
func OnlyError() error { return nil }

// @Bean
func TwoValues() (A, B) { return A{}, B{} }

// @Bean
func ErrorFirst() (error, A) { return nil, A{} }

// @Bean
func MultipleErrors() (A, error, error) { return A{}, nil, nil }

// @Bean
func Variadic(values ...A) B { return B{} }

// @Bean
func Generic[T any]() T { var zero T; return zero }

// @Bean
func (A) Method() B { return B{} }
`,
	})
	program, resolved := loadAndResolve(t, root, ".")
	catalog := buildQuiet(t, program, resolved)
	joined := strings.Join(diagnosticStrings(catalog.Diagnostics()), "\n")
	for _, expected := range []string{
		"must return one provided value",
		"error cannot be the only result",
		"may return only one provided value",
		"error must be the final and only additional result",
		"at most one error result",
		"variadic provider functions are not supported",
		"generic provider functions are not supported",
		"must target a package-level function",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("diagnostics missing %q:\n%s", expected, joined)
		}
	}
	if len(catalog.Providers()) != 0 {
		t.Fatalf("invalid providers entered catalog: %#v", catalog.Providers())
	}
}

func TestCatalogRejectsDuplicateOutput(t *testing.T) {
	root := writeModule(t, map[string]string{
		"go.mod":           "module example.com/duplicates\n\ngo 1.23.0\n",
		"shared/shared.go": "package shared\n\ntype Config struct{}\n",
		"a/a.go": `package a
import "example.com/duplicates/shared"
// @Bean
func First() shared.Config { return shared.Config{} }
`,
		"a/z.go": `package a
import "example.com/duplicates/shared"
// @Bean
func Second() shared.Config { return shared.Config{} }
`,
		"b/b.go": `package b
import "example.com/duplicates/shared"
// @Bean
func Third() shared.Config { return shared.Config{} }
`,
	})
	program, resolved := loadAndResolve(t, root, "./...")
	catalog := buildQuiet(t, program, resolved)
	diagnostics := catalog.Diagnostics()
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics = %v, want one duplicate diagnostic", diagnosticStrings(diagnostics))
	}
	message := diagnostics[0].Error()
	for _, expected := range []string{
		"exact type example.com/duplicates/shared.Config",
		"example.com/duplicates/a.First",
		"example.com/duplicates/a.Second",
		"example.com/duplicates/b.Third",
		"qualifiers and implicit interface bindings are not supported",
	} {
		if !strings.Contains(message, expected) {
			t.Fatalf("diagnostic %q missing %q", message, expected)
		}
	}
}

func TestCatalogRejectsAliasDuplicateOutput(t *testing.T) {
	root := writeModule(t, map[string]string{
		"go.mod": "module example.com/aliasduplicates\n\ngo 1.23.0\n",
		"providers.go": `package aliasduplicates

type Original struct{}
type Alias = Original
type Distinct Original

// @Bean
func OriginalProvider() Original { panic("provider body must not execute") }

// @Bean
func AliasProvider() Alias { panic("provider body must not execute") }

// @Bean
func DistinctProvider() Distinct { panic("provider body must not execute") }
`,
	})
	program, resolved := loadAndResolve(t, root, ".")
	catalog := buildQuiet(t, program, resolved)
	providers := catalog.Providers()
	original := providerByName(providers, "OriginalProvider")
	alias := providerByName(providers, "AliasProvider")
	distinct := providerByName(providers, "DistinctProvider")
	if original == nil || alias == nil || distinct == nil {
		t.Fatalf("providers = %#v", providers)
	}
	if !types.Identical(original.Output, alias.Output) {
		t.Fatalf("alias output %q is not identical to original output %q", alias.OutputTypeID, original.OutputTypeID)
	}
	if original.OutputTypeID == alias.OutputTypeID {
		t.Fatalf("fixture does not exercise distinct alias display IDs: both are %q", original.OutputTypeID)
	}
	if types.Identical(original.Output, distinct.Output) {
		t.Fatalf("distinct named output %q unexpectedly conflicts with %q", distinct.OutputTypeID, original.OutputTypeID)
	}

	diagnostics := catalog.Diagnostics()
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics = %v, want one alias duplicate diagnostic", diagnosticStrings(diagnostics))
	}
	message := diagnostics[0].Error()
	for _, expected := range []string{
		"exact type example.com/aliasduplicates.Alias",
		"example.com/aliasduplicates.AliasProvider",
		"example.com/aliasduplicates.OriginalProvider",
		"qualifiers and implicit interface bindings are not supported",
	} {
		if !strings.Contains(message, expected) {
			t.Fatalf("diagnostic %q missing %q", message, expected)
		}
	}
	if strings.Contains(message, "DistinctProvider") {
		t.Fatalf("distinct named provider incorrectly entered alias duplicate diagnostic: %q", message)
	}
}

func TestCatalogDeterministic(t *testing.T) {
	root := writeModule(t, map[string]string{
		"go.mod": "module example.com/deterministic\n\ngo 1.23.0\n",
		"z.go": `package deterministic

type Z struct{}
// @Bean
func ZProvider() Z { return Z{} }
`,
		"a.go": `package deterministic

type A struct{}
// @Bean
func AProvider() A { return A{} }
`,
	})
	var first string
	for run := 0; run < 5; run++ {
		program, resolved := loadAndResolve(t, root, ".")
		catalog := buildQuiet(t, program, resolved)
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
	if !strings.Contains(first, "AProvider") || !strings.Contains(first, "ZProvider") || strings.Index(first, "AProvider") > strings.Index(first, "ZProvider") {
		t.Fatalf("provider summary is not stable symbol order: %s", first)
	}
}

func buildQuiet(t *testing.T, program *load.Program, resolved resolve.Result) Catalog {
	t.Helper()
	stdoutReader, stdoutWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stderrReader, stderrWriter, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	originalStdout, originalStderr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = stdoutWriter, stderrWriter
	catalog := Build(program, resolved)
	os.Stdout, os.Stderr = originalStdout, originalStderr
	if err := stdoutWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stderrWriter.Close(); err != nil {
		t.Fatal(err)
	}
	stdout, err := io.ReadAll(stdoutReader)
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := io.ReadAll(stderrReader)
	if err != nil {
		t.Fatal(err)
	}
	if err := stdoutReader.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stderrReader.Close(); err != nil {
		t.Fatal(err)
	}
	if len(stdout) != 0 || len(stderr) != 0 {
		t.Fatalf("provider library wrote stdout=%q stderr=%q", stdout, stderr)
	}
	return catalog
}

func loadAndResolve(t *testing.T, root string, patterns ...string) (*load.Program, resolve.Result) {
	t.Helper()
	program, err := load.Load(context.Background(), load.Options{Dir: root}, patterns...)
	if err != nil {
		t.Fatalf("load.Load() error = %v", err)
	}
	resolved := resolve.Annotations(program)
	if len(resolved.Diagnostics) != 0 {
		t.Fatalf("resolve.Annotations() diagnostics = %v", resolved.Diagnostics)
	}
	return program, resolved
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

func providerByName(providers []Provider, name string) *Provider {
	for i := range providers {
		if providers[i].Name == name {
			return &providers[i]
		}
	}
	return nil
}

func diagnosticStrings(diagnostics []Diagnostic) []string {
	result := make([]string, len(diagnostics))
	for i, diagnostic := range diagnostics {
		result[i] = diagnostic.Error()
	}
	return result
}

func catalogSummary(catalog Catalog) string {
	var builder strings.Builder
	for _, provider := range catalog.Providers() {
		builder.WriteString(provider.SymbolID)
		builder.WriteByte('|')
		builder.WriteString(provider.OutputTypeID)
		fmt.Fprintf(&builder, "|cleanup=%t|error=%t", provider.ReturnsCleanup, provider.ReturnsError)
		for _, dependency := range provider.Dependencies {
			builder.WriteByte('|')
			builder.WriteString(dependency.TypeID)
		}
		builder.WriteByte('\n')
	}
	for _, diagnostic := range catalog.Diagnostics() {
		builder.WriteString(diagnostic.Error())
		builder.WriteByte('\n')
	}
	return builder.String()
}

func TestCatalogAcceptsCleanupSignatures(t *testing.T) {
	root := writeModule(t, map[string]string{
		"go.mod": "module github.com/StevenBuglione/spice\n\ngo 1.23.0\n",
		"lifecycle/cleanup.go": `package lifecycle
import "context"
type Cleanup func(context.Context) error
`,
		"app/providers.go": `package app

import life "github.com/StevenBuglione/spice/lifecycle"

type Config struct{}
type PlainValue struct{}
type ErrorValue struct{}
type CleanupValue struct{}
type CleanupErrorValue struct{}
type AliasValue struct{}
type AliasErrorValue struct{}
type CleanupAlias = life.Cleanup
type ErrorAlias = error

// @Bean
func ConfigProvider() Config { panic("provider body must not execute") }

// @Bean
func PlainProvider() PlainValue { panic("provider body must not execute") }

// @Bean
func ErrorProvider(Config) (ErrorValue, error) { panic("provider body must not execute") }

// @Bean
func CleanupProvider(Config) (CleanupValue, life.Cleanup) {
	panic("provider and cleanup bodies must not execute")
}

// @Bean
func CleanupErrorProvider(Config) (CleanupErrorValue, life.Cleanup, error) {
	panic("provider and cleanup bodies must not execute")
}

// @Bean
func AliasProvider(Config) (AliasValue, CleanupAlias) {
	panic("provider and cleanup bodies must not execute")
}

// @Bean
func AliasErrorProvider(Config) (AliasErrorValue, CleanupAlias, ErrorAlias) {
	panic("provider and cleanup bodies must not execute")
}

// @Bean
func CleanupAsOutputProvider() life.Cleanup {
	panic("provider body must not execute")
}
`,
	})

	program, resolved := loadAndResolve(t, root, "./app")
	catalog := buildQuiet(t, program, resolved)
	if diagnostics := catalog.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("Build() diagnostics = %v", diagnosticStrings(diagnostics))
	}
	providers := catalog.Providers()
	checks := map[string]struct {
		cleanup bool
		err     bool
	}{
		"PlainProvider":           {},
		"ErrorProvider":           {err: true},
		"CleanupProvider":         {cleanup: true},
		"CleanupErrorProvider":    {cleanup: true, err: true},
		"AliasProvider":           {cleanup: true},
		"AliasErrorProvider":      {cleanup: true, err: true},
		"CleanupAsOutputProvider": {},
	}
	for name, want := range checks {
		item := providerByName(providers, name)
		if item == nil {
			t.Fatalf("missing provider %s in %#v", name, providers)
		}
		if item.ReturnsCleanup != want.cleanup || item.ReturnsError != want.err {
			t.Fatalf("%s flags cleanup=%v error=%v, want cleanup=%v error=%v", name, item.ReturnsCleanup, item.ReturnsError, want.cleanup, want.err)
		}
	}
	cleanupOutput := providerByName(providers, "CleanupAsOutputProvider")
	if cleanupOutput.OutputTypeID != "github.com/StevenBuglione/spice/lifecycle.Cleanup" || cleanupOutput.ReturnsCleanup {
		t.Fatalf("cleanup primary output = %#v", cleanupOutput)
	}
	cleanupProvider := providerByName(providers, "CleanupProvider")
	if len(cleanupProvider.Dependencies) != 1 || cleanupProvider.Dependencies[0].TypeID != "github.com/StevenBuglione/spice/app.Config" {
		t.Fatalf("cleanup provider dependencies = %#v", cleanupProvider.Dependencies)
	}
}

func TestCatalogRejectsInvalidCleanupSignatures(t *testing.T) {
	root := writeModule(t, map[string]string{
		"go.mod": "module github.com/StevenBuglione/spice\n\ngo 1.23.0\n",
		"lifecycle/cleanup.go": `package lifecycle
import "context"
type Cleanup func(context.Context) error
`,
		"app/providers.go": `package app

import (
	"context"
	life "github.com/StevenBuglione/spice/lifecycle"
)

type Value struct{}
type OtherCleanup func(context.Context) error

// @Bean
func UnnamedCleanup() (Value, func(context.Context) error) { panic("must not execute") }

// @Bean
func DistinctCleanup() (Value, OtherCleanup) { panic("must not execute") }

// @Bean
func WrongShape() (Value, func() error) { panic("must not execute") }

// @Bean
func ErrorBeforeCleanup() (Value, error, life.Cleanup) { panic("must not execute") }

// @Bean
func TwoCleanup() (Value, life.Cleanup, life.Cleanup) { panic("must not execute") }

// @Bean
func TwoErrors() (Value, error, error) { panic("must not execute") }

// @Bean
func ExtraResult() (Value, life.Cleanup, error, int) { panic("must not execute") }

// @Bean
func CleanupInFinalPosition() (Value, int, life.Cleanup) { panic("must not execute") }
`,
	})

	program, resolved := loadAndResolve(t, root, "./app")
	catalog := buildQuiet(t, program, resolved)
	if len(catalog.Providers()) != 0 {
		t.Fatalf("invalid cleanup providers entered catalog: %#v", catalog.Providers())
	}
	joined := strings.Join(diagnosticStrings(catalog.Diagnostics()), "\n")
	for _, expected := range []string{
		"UnnamedCleanup", "DistinctCleanup", "WrongShape", "ErrorBeforeCleanup",
		"TwoCleanup", "TwoErrors", "ExtraResult", "CleanupInFinalPosition",
		"lifecycle.Cleanup", "accepted forms are",
	} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("diagnostics missing %q:\n%s", expected, joined)
		}
	}
}

func TestCatalogCleanupMetadataDeterministic(t *testing.T) {
	files := map[string]string{
		"go.mod": "module github.com/StevenBuglione/spice\n\ngo 1.23.0\n",
		"lifecycle/cleanup.go": `package lifecycle
import "context"
type Cleanup func(context.Context) error
`,
		"app/z.go": `package app
import life "github.com/StevenBuglione/spice/lifecycle"
type Z struct{}
// @Bean
func ZProvider() (Z, life.Cleanup) { panic("must not execute") }
`,
		"app/a.go": `package app
import life "github.com/StevenBuglione/spice/lifecycle"
type A struct{}
// @Bean
func AProvider() (A, life.Cleanup, error) { panic("must not execute") }
`,
	}
	var first string
	for run := 0; run < 20; run++ {
		program, resolved := loadAndResolve(t, writeModule(t, files), "./app")
		catalog := buildQuiet(t, program, resolved)
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
	if !strings.Contains(first, "cleanup=true|error=false") || !strings.Contains(first, "cleanup=true|error=true") {
		t.Fatalf("cleanup flags absent from deterministic summary: %s", first)
	}
}
