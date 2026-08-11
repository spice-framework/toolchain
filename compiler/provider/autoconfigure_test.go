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

func TestSelectAutoConfigurationAdmitsCompilerFeatureDependency(t *testing.T) {
	t.Parallel()

	loggerType := types.NewPointer(testNamedType("Logger"))
	processorType := testNamedType("Processor")
	processor := Provider{
		Source: SourceAutoConfiguration, SymbolID: "logging-processor",
		Name: "loggingProcessor", Output: processorType,
		OutputTypeID: "example.com/client.Processor",
		Dependencies: []Dependency{{
			Kind: DependencySingle, Type: loggerType,
			TypeID: "*example.com/client.Logger",
		}},
	}
	defaults := Catalog{providers: []Provider{processor}}

	selected, decisions := SelectAutoConfiguration(Catalog{}, defaults)
	if len(selected.Providers()) != 0 || len(decisions) != 1 ||
		decisions[0].Status != AutoConfigurationInactive {
		t.Fatalf("selection without feature = %#v, %#v", selected.Providers(), decisions)
	}
	selected, decisions = SelectAutoConfigurationWithAvailable(
		Catalog{}, defaults, loggerType,
	)
	if len(selected.Providers()) != 1 || len(decisions) != 1 ||
		decisions[0].Status != AutoConfigurationSelected {
		t.Fatalf("selection with feature = %#v, %#v", selected.Providers(), decisions)
	}
}

func TestSelectAutoConfigurationExtendsRepeatedOutputCollectionsByName(
	t *testing.T,
) {
	t.Parallel()

	handlerType := testNamedType("Handler")
	primary := Catalog{providers: []Provider{
		{
			Source:       SourceBean,
			SymbolID:     "custom",
			Name:         "customHandler",
			Output:       handlerType,
			OutputTypeID: "example.com/client.Handler",
		},
		{
			Source:       SourceBean,
			SymbolID:     "replace-version",
			Name:         "versionHandler",
			Output:       handlerType,
			OutputTypeID: "example.com/client.Handler",
		},
	}}
	defaults := Catalog{providers: []Provider{
		{
			Source:       SourceAutoConfiguration,
			SymbolID:     "default-help",
			Name:         "helpHandler",
			Output:       handlerType,
			OutputTypeID: "example.com/client.Handler",
		},
		{
			Source:       SourceAutoConfiguration,
			SymbolID:     "default-version",
			Name:         "versionHandler",
			Output:       handlerType,
			OutputTypeID: "example.com/client.Handler",
		},
	}}

	selected, decisions := SelectAutoConfiguration(primary, defaults)
	providers := selected.Providers()
	if len(providers) != 1 || providers[0].Name != "helpHandler" {
		t.Fatalf("selected defaults = %#v, want only helpHandler", providers)
	}
	if len(decisions) != 2 ||
		decisions[0].Provider.Name != "helpHandler" ||
		decisions[0].Status != AutoConfigurationSelected ||
		decisions[1].Provider.Name != "versionHandler" ||
		decisions[1].Status != AutoConfigurationReplaced ||
		!strings.Contains(decisions[1].Reason, "versionHandler") {
		t.Fatalf("decisions = %#v", decisions)
	}
}

func TestSelectAutoConfigurationKeepsSingleOutputTypeBackoff(t *testing.T) {
	t.Parallel()

	output := testNamedType("Client")
	selected, decisions := SelectAutoConfiguration(
		Catalog{providers: []Provider{{
			Source:       SourceBean,
			SymbolID:     "application-client",
			Name:         "custom",
			Output:       output,
			OutputTypeID: "example.com/client.Client",
		}}},
		Catalog{providers: []Provider{{
			Source:       SourceAutoConfiguration,
			SymbolID:     "default-client",
			Name:         "defaultClient",
			Output:       output,
			OutputTypeID: "example.com/client.Client",
		}}},
	)
	if len(selected.Providers()) != 0 ||
		len(decisions) != 1 ||
		decisions[0].Status != AutoConfigurationReplaced {
		t.Fatalf(
			"selected = %#v, decisions = %#v",
			selected.Providers(),
			decisions,
		)
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
