package graph

import (
	"go/types"
	"slices"
	"strings"
	"testing"

	"github.com/StevenBuglione/spice/annotation/sdk"
	"github.com/StevenBuglione/spice/compiler/provider"
)

func TestGraphSelectsQualifiersPrimaryFallbackAndParameterNames(
	t *testing.T,
) {
	t.Parallel()
	stringType := types.Typ[types.String]
	intType := types.Typ[types.Int]
	catalog := provider.Add(
		provider.Catalog{},
		provider.Provider{
			SymbolID:     "stripe",
			Name:         "stripe",
			Aliases:      []string{"card"},
			Qualifiers:   []string{"payments"},
			Primary:      true,
			Scope:        sdk.BeanScopeSingleton,
			Output:       stringType,
			OutputTypeID: "string",
		},
		provider.Provider{
			SymbolID:     "offline",
			Name:         "offline",
			Fallback:     true,
			Scope:        sdk.BeanScopeSingleton,
			Output:       stringType,
			OutputTypeID: "string",
		},
		provider.Provider{
			SymbolID:     "qualified",
			Name:         "qualified",
			Scope:        sdk.BeanScopeSingleton,
			Output:       types.Typ[types.Bool],
			OutputTypeID: "bool",
			Dependencies: []provider.Dependency{{
				Index:      0,
				Name:       "processor",
				Type:       stringType,
				TypeID:     "string",
				Kind:       provider.DependencySingle,
				Qualifiers: []string{"card"},
			}},
		},
		provider.Provider{
			SymbolID:     "default",
			Name:         "default",
			Scope:        sdk.BeanScopeSingleton,
			Output:       types.Typ[types.Float64],
			OutputTypeID: "float64",
			Dependencies: []provider.Dependency{{
				Index:  0,
				Name:   "processor",
				Type:   stringType,
				TypeID: "string",
				Kind:   provider.DependencySingle,
			}},
		},
		provider.Provider{
			SymbolID:     "first",
			Name:         "first",
			Scope:        sdk.BeanScopeSingleton,
			Output:       intType,
			OutputTypeID: "int",
		},
		provider.Provider{
			SymbolID:     "second",
			Name:         "second",
			Aliases:      []string{"chosen"},
			Scope:        sdk.BeanScopeSingleton,
			Output:       intType,
			OutputTypeID: "int",
		},
		provider.Provider{
			SymbolID:     "named",
			Name:         "named",
			Scope:        sdk.BeanScopeSingleton,
			Output:       types.Typ[types.Complex64],
			OutputTypeID: "complex64",
			Dependencies: []provider.Dependency{{
				Index:  0,
				Name:   "chosen",
				Type:   intType,
				TypeID: "int",
				Kind:   provider.DependencySingle,
			}},
		},
	)
	result := Build(catalog)
	if diagnostics := result.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", diagnostics)
	}
	selected := make(map[string]string)
	for _, edge := range result.Edges() {
		selected[edge.ConsumerID] = edge.DependencyID
	}
	if selected["qualified"] != "stripe" ||
		selected["default"] != "stripe" ||
		selected["named"] != "second" {
		t.Fatalf("selection = %#v", selected)
	}
}

func TestGraphInjectsOrderedCollectionsAndPermitsEmptyOptional(
	t *testing.T,
) {
	t.Parallel()
	element := types.Typ[types.String]
	catalog := provider.Add(
		provider.Catalog{},
		provider.Provider{
			SymbolID: "last", Name: "last", Order: 20,
			Output: element, OutputTypeID: "string",
		},
		provider.Provider{
			SymbolID: "first", Name: "first", Order: -10,
			Output: element, OutputTypeID: "string",
		},
		provider.Provider{
			SymbolID: "middle", Name: "middle",
			Output: element, OutputTypeID: "string",
		},
		provider.Provider{
			SymbolID: "consumer", Name: "consumer",
			Output: types.Typ[types.Bool], OutputTypeID: "bool",
			Dependencies: []provider.Dependency{
				{
					Index: 0, Name: "all",
					Type: types.NewSlice(element), TypeID: "[]string",
					Kind:    provider.DependencySlice,
					Element: element, ElementTypeID: "string",
				},
				{
					Index: 1, Name: "missing",
					Type:          types.Typ[types.Uint64],
					TypeID:        "bean.Optional[uint64]",
					Kind:          provider.DependencyOptional,
					Element:       types.Typ[types.Uint64],
					ElementTypeID: "uint64",
				},
			},
		},
	)
	result := Build(catalog)
	if diagnostics := result.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %v", diagnostics)
	}
	var order []string
	for _, edge := range result.Edges() {
		if edge.ConsumerID == "consumer" &&
			edge.ParameterIndex == 0 {
			order = append(order, edge.DependencyID)
		}
	}
	if !slices.Equal(
		order,
		[]string{"first", "middle", "last"},
	) {
		t.Fatalf("collection order = %v", order)
	}
}

func TestGraphRequiresProviderHandleForNarrowerScopes(t *testing.T) {
	t.Parallel()
	valueType := types.Typ[types.String]
	scoped := provider.Provider{
		SymbolID: "request", Name: "request",
		Scope:  sdk.BeanScopeRequest,
		Output: valueType, OutputTypeID: "string",
	}
	direct := provider.Provider{
		SymbolID: "direct", Name: "direct",
		Output: types.Typ[types.Bool], OutputTypeID: "bool",
		Dependencies: []provider.Dependency{{
			Index: 0, Name: "value", Type: valueType,
			TypeID: "string", Kind: provider.DependencySingle,
		}},
	}
	result := Build(provider.Add(
		provider.Catalog{},
		scoped,
		direct,
	))
	diagnostics := result.Diagnostics()
	if len(diagnostics) != 1 ||
		diagnostics[0].Kind != "scope-mismatch" ||
		!strings.Contains(
			diagnostics[0].Message,
			"bean.Provider[string]",
		) {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}

	handle := direct
	handle.SymbolID = "handle"
	handle.Name = "handle"
	handle.Dependencies[0].Kind = provider.DependencyProvider
	handle.Dependencies[0].Element = valueType
	handle.Dependencies[0].ElementTypeID = "string"
	result = Build(provider.Add(
		provider.Catalog{},
		scoped,
		handle,
	))
	if diagnostics := result.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("handle diagnostics = %#v", diagnostics)
	}
	if edges := result.Edges(); len(edges) != 1 ||
		edges[0].DependencyID != "request" ||
		edges[0].DependencyKind != provider.DependencyProvider {
		t.Fatalf("handle edges = %#v", edges)
	}
}
