package provider

import (
	"go/token"
	"go/types"
	"path"
	"slices"
	"strings"
	"testing"
)

func TestSelectAutoConfigurationPrunesReplacementAndUnavailableInputs(t *testing.T) {
	clientType := testNamedType("Client")
	optionsType := testNamedType("Options")
	applicationClient := Provider{
		Source:       SourceBean,
		SymbolID:     "application-client",
		Name:         "client",
		Output:       clientType,
		OutputTypeID: "example.com/client.Client",
	}
	replacedDefault := Provider{
		Source:       SourceAutoConfiguration,
		SymbolID:     "default-client",
		Name:         "defaultClient",
		Output:       clientType,
		OutputTypeID: "example.com/client.Client",
	}
	inactiveDefault := Provider{
		Source:       SourceAutoConfiguration,
		SymbolID:     "dependent-client",
		Name:         "dependentClient",
		Output:       testNamedType("Dependent"),
		OutputTypeID: "example.com/client.Dependent",
		Dependencies: []Dependency{{
			Kind:   DependencySingle,
			Type:   optionsType,
			TypeID: "example.com/client.Options",
		}},
	}
	selected, decisions := SelectAutoConfiguration(
		Catalog{providers: []Provider{applicationClient}},
		Catalog{providers: []Provider{replacedDefault, inactiveDefault}},
	)
	if len(selected.Providers()) != 0 {
		t.Fatalf("selected defaults = %+v, want none", selected.Providers())
	}
	if len(decisions) != 2 ||
		decisions[0].Status != AutoConfigurationReplaced ||
		decisions[1].Status != AutoConfigurationInactive ||
		!strings.Contains(decisions[1].Reason, "example.com/client.Options") {
		t.Fatalf("decisions = %+v", decisions)
	}
}

func TestSelectAutoConfigurationBuildsDependencyClosure(t *testing.T) {
	optionsType := testNamedType("Options")
	clientType := testNamedType("Client")
	options := Provider{
		Source:       SourceAutoConfiguration,
		SymbolID:     "options",
		Name:         "options",
		Output:       optionsType,
		OutputTypeID: "example.com/client.Options",
	}
	client := Provider{
		Source:       SourceAutoConfiguration,
		SymbolID:     "client",
		Name:         "client",
		Output:       clientType,
		OutputTypeID: "example.com/client.Client",
		Dependencies: []Dependency{{
			Kind:   DependencySingle,
			Type:   optionsType,
			TypeID: "example.com/client.Options",
		}},
	}
	selected, decisions := SelectAutoConfiguration(
		Catalog{},
		Catalog{providers: []Provider{client, options}},
	)
	providers := selected.Providers()
	if len(providers) != 2 ||
		!slices.Equal(
			[]string{providers[0].SymbolID, providers[1].SymbolID},
			[]string{"client", "options"},
		) {
		t.Fatalf("selected providers = %+v", providers)
	}
	for _, decision := range decisions {
		if decision.Status != AutoConfigurationSelected {
			t.Fatalf("decision = %+v", decision)
		}
	}
}

func TestSelectAutoConfigurationMatchesInterfacesAndPermissiveInputs(t *testing.T) {
	interfaceType := types.NewNamed(
		types.NewTypeName(
			token.NoPos,
			types.NewPackage("example.com/client", "client"),
			"Contract",
			nil,
		),
		types.NewInterfaceType(nil, nil).Complete(),
		nil,
	)
	implementationType := testNamedType("Implementation")
	consumerType := testNamedType("Consumer")
	implementation := Provider{
		Source:       SourceBean,
		SymbolID:     "implementation",
		Name:         "implementation",
		Output:       implementationType,
		OutputTypeID: "example.com/client.Implementation",
		Interfaces: []InterfaceBinding{{
			Type:   interfaceType,
			TypeID: "example.com/client.Contract",
		}},
	}
	consumer := Provider{
		Source:       SourceAutoConfiguration,
		SymbolID:     "consumer",
		Name:         "consumer",
		Output:       consumerType,
		OutputTypeID: "example.com/client.Consumer",
		Dependencies: []Dependency{
			{
				Kind:   DependencySingle,
				Type:   interfaceType,
				TypeID: "example.com/client.Contract",
			},
			{
				Kind:          DependencyOptional,
				Type:          testNamedType("Optional"),
				TypeID:        "bean.Optional[example.com/client.Missing]",
				ElementTypeID: "example.com/client.Missing",
			},
			{
				Kind:          DependencySlice,
				Type:          types.NewSlice(testNamedType("Collection")),
				TypeID:        "[]example.com/client.Collection",
				ElementTypeID: "example.com/client.Collection",
			},
		},
	}
	selected, decisions := SelectAutoConfiguration(
		Catalog{providers: []Provider{implementation}},
		Catalog{providers: []Provider{consumer}},
	)
	if len(selected.Providers()) != 1 ||
		len(decisions) != 1 ||
		decisions[0].Status != AutoConfigurationSelected {
		t.Fatalf("selected = %+v, decisions = %+v", selected.Providers(), decisions)
	}
}

func testNamedType(name string) *types.Named {
	const packagePath = "example.com/client"
	pkg := types.NewPackage(packagePath, path.Base(packagePath))
	return types.NewNamed(
		types.NewTypeName(token.NoPos, pkg, name, nil),
		types.NewStruct(nil, nil),
		nil,
	)
}
