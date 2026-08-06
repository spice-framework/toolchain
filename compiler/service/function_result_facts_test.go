package service

import (
	"context"
	"go/types"
	"os"
	"path/filepath"
	"testing"

	"github.com/spice-framework/spice/annotation"
	"github.com/spice-framework/spice/annotation/sdk"
	"github.com/spice-framework/toolchain/compiler/descriptor"
	"github.com/spice-framework/toolchain/compiler/load"
	"github.com/spice-framework/toolchain/compiler/provider"
	"github.com/spice-framework/toolchain/compiler/resolve"
)

func TestToolAnalyzeParamsDescribesGenericFunctionResults(t *testing.T) {
	root := writeFunctionResultFixture(t)
	program, err := load.Load(context.Background(), load.Options{
		Dir: root,
		Env: append(
			os.Environ(),
			"GOWORK=off",
			"GOPROXY=off",
		),
	}, "./...")
	if err != nil {
		t.Fatalf("load result-fact fixture: %v", err)
	}
	symbols := make(map[string]load.Symbol)
	for _, symbol := range program.Symbols() {
		if symbol.PackagePath == "example.com/resultfacts/app" {
			symbols[symbol.Name] = symbol
		}
	}

	tests := []struct {
		name    string
		results []sdk.FunctionResultFact
	}{
		{
			name: "Direct",
			results: []sdk.FunctionResultFact{{
				TypeID:             "example.com/resultfacts/contracts.Direct",
				CanonicalTypeID:    "example.com/resultfacts/contracts.Direct",
				Kind:               sdk.GoTypeInterface,
				NamedOriginPackage: "example.com/resultfacts/contracts",
				NamedOriginName:    "Direct",
			}},
		},
		{
			name: "Alias",
			results: []sdk.FunctionResultFact{{
				TypeID:             "example.com/resultfacts/contracts.Alias",
				CanonicalTypeID:    "example.com/resultfacts/contracts.Direct",
				Kind:               sdk.GoTypeInterface,
				NamedOriginPackage: "example.com/resultfacts/contracts",
				NamedOriginName:    "Direct",
			}},
		},
		{
			name: "Stage",
			results: []sdk.FunctionResultFact{{
				TypeID:             "example.com/resultfacts/contracts.Stage[string, int]",
				CanonicalTypeID:    "example.com/resultfacts/contracts.Stage[string, int]",
				Kind:               sdk.GoTypeInterface,
				NamedOriginPackage: "example.com/resultfacts/contracts",
				NamedOriginName:    "Stage",
			}},
		},
		{
			name: "Wrapper",
			results: []sdk.FunctionResultFact{{
				TypeID:             "example.com/resultfacts/contracts.Wrapper",
				CanonicalTypeID:    "example.com/resultfacts/contracts.Wrapper",
				Kind:               sdk.GoTypeInterface,
				NamedOriginPackage: "example.com/resultfacts/contracts",
				NamedOriginName:    "Wrapper",
			}},
		},
		{
			name: "Concrete",
			results: []sdk.FunctionResultFact{{
				TypeID:             "example.com/resultfacts/contracts.Record",
				CanonicalTypeID:    "example.com/resultfacts/contracts.Record",
				Kind:               sdk.GoTypeStruct,
				NamedOriginPackage: "example.com/resultfacts/contracts",
				NamedOriginName:    "Record",
			}},
		},
		{
			name: "Pointer",
			results: []sdk.FunctionResultFact{{
				TypeID:          "*example.com/resultfacts/contracts.Record",
				CanonicalTypeID: "*example.com/resultfacts/contracts.Record",
				Kind:            sdk.GoTypePointer,
			}},
		},
		{
			name: "CleanupError",
			results: []sdk.FunctionResultFact{
				{TypeID: "int", CanonicalTypeID: "int", Kind: sdk.GoTypeBasic},
				{
					TypeID:             "example.com/resultfacts/contracts.Cleanup",
					CanonicalTypeID:    "example.com/resultfacts/contracts.Cleanup",
					Kind:               sdk.GoTypeSignature,
					NamedOriginPackage: "example.com/resultfacts/contracts",
					NamedOriginName:    "Cleanup",
				},
				{
					TypeID:          "error",
					CanonicalTypeID: "error",
					Kind:            sdk.GoTypeInterface,
					NamedOriginName: "error",
				},
			},
		},
		{
			name: "Generic",
			results: []sdk.FunctionResultFact{{
				TypeID: "T", CanonicalTypeID: "T", Kind: sdk.GoTypeTypeParameter,
			}},
		},
		{name: "Zero", results: []sdk.FunctionResultFact{}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			symbol, found := symbols[test.name]
			if !found {
				t.Fatalf("loaded symbol %q was not found", test.name)
			}
			params, analyzeErr := toolAnalyzeParams(
				descriptor.Descriptor{Package: "example.com/fixture", Symbol: "Fact"},
				resolve.Occurrence{
					Target:      annotation.TargetFunction,
					Name:        symbol.Name,
					SymbolID:    symbol.ID,
					PackagePath: symbol.PackagePath,
				},
				symbol,
			)
			if analyzeErr != nil {
				t.Fatalf("toolAnalyzeParams() error = %v", analyzeErr)
			}
			if params.Invocation.Declaration.TypeID != provider.TypeID(symbol.Object.Type()) {
				t.Fatalf(
					"declaration TypeID = %q, want unchanged %q",
					params.Invocation.Declaration.TypeID,
					provider.TypeID(symbol.Object.Type()),
				)
			}
			got, present, decodeErr := params.Invocation.FunctionResultFacts()
			if decodeErr != nil {
				t.Fatalf("FunctionResultFacts() error = %v", decodeErr)
			}
			if !present {
				t.Fatal("FunctionResultFacts() present = false, want true")
			}
			assertFunctionResultFacts(t, got, test.results)
		})
	}
}

func TestToolFunctionResultFactsOmitsNonFunctionDeclarations(t *testing.T) {
	facts, err := toolFunctionResultFacts(nil)
	if err != nil {
		t.Fatalf("toolFunctionResultFacts(nil) error = %v", err)
	}
	if len(facts) != 0 {
		t.Fatalf("toolFunctionResultFacts(nil) = %#v, want empty", facts)
	}
	if _, err := toolGoTypeKind(types.NewNamed(
		types.NewTypeName(0, nil, "Unexpected", nil),
		types.NewStruct(nil, nil),
		nil,
	)); err == nil {
		t.Fatal("toolGoTypeKind(named) error = nil, want defensive error")
	}
}

func assertFunctionResultFacts(
	t *testing.T,
	got []sdk.FunctionResultFact,
	want []sdk.FunctionResultFact,
) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("function results = %+v, want %+v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Errorf("function result %d = %+v, want %+v", index, got[index], want[index])
		}
	}
}

func writeFunctionResultFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFixtureFile(t, root, "go.mod", "module example.com/resultfacts\n\ngo 1.26.0\n")
	writeFixtureFile(t, root, "contracts/contracts.go", `package contracts

import "context"

type Direct interface { Value() string }
type Alias = Direct
type Wrapper interface { Direct }
type Stage[Input, Output any] interface {
	Process(context.Context, Input) (Output, error)
}
type Record struct{}
type Cleanup func(context.Context) error
`)
	writeFixtureFile(t, root, "app/app.go", `package app

import "example.com/resultfacts/contracts"

func Direct() contracts.Direct { return nil }
func Alias() contracts.Alias { return nil }
func Stage() contracts.Stage[string, int] { return nil }
func Wrapper() contracts.Wrapper { return nil }
func Concrete() contracts.Record { return contracts.Record{} }
func Pointer() *contracts.Record { return nil }
func CleanupError() (int, contracts.Cleanup, error) { return 0, nil, nil }
func Generic[T any]() T { var zero T; return zero }
func Zero() {}
`)
	return root
}

func writeFixtureFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create fixture directory: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture file %s: %v", name, err)
	}
}
