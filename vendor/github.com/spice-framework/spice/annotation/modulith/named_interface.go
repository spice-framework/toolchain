package modulith

import (
	"context"

	"github.com/spice-framework/spice/annotation/coretool"
	"github.com/spice-framework/spice/annotation/sdk"
)

// NamedInterface exposes one descendant package as a named module API.
//
// The descriptor is repeatable on package documentation. Consumers reference
// the owning module and API explicitly; a named interface never exposes other
// descendant packages by implication.
//
//	// @import { NamedInterface } from "github.com/spice-framework/spice/annotation/modulith"
//	// @NamedInterface("events")
//	package api
func NamedInterface() sdk.Definition {
	return sdk.Definition{
		Name:       "modulith.NamedInterface",
		Summary:    "Exposes one package as a named module API.",
		Targets:    []sdk.Target{sdk.TargetPackage},
		Repeatable: true,
		Arguments: []sdk.Argument{{
			Name:        "name",
			Kinds:       []sdk.Kind{sdk.KindString},
			Description: "Stable API name exported by the owning module.",
			Required:    true,
			Positional:  true,
		}},
		Examples: []sdk.Example{{
			Title: "Named API",
			Code:  "// @NamedInterface(\"events\")\npackage api",
		}},
		Compatibility: sdk.Compatibility{
			Since:        "0.1.0",
			MinimumSpice: "0.1.0",
		},
		Implementation: sdk.Implementation{
			Tool:     coretool.Path,
			Handler:  NamedInterfaceHandler,
			Protocol: sdk.ProtocolV1Alpha2,
		},
	}
}

// NamedInterfaceHandler contributes one explicitly exposed module API.
func NamedInterfaceHandler(
	_ context.Context,
	invocation sdk.Invocation,
) (sdk.Result, error) {
	if err := invocation.RequireDescriptor(
		"github.com/spice-framework/spice/annotation/modulith",
		"NamedInterface",
	); err != nil {
		return sdk.Result{}, err
	}
	arguments, err := sdk.BindArguments(invocation, "name", "name")
	if err != nil {
		return sdk.Result{}, err
	}
	name, err := arguments.String("name", true)
	if err != nil {
		return sdk.Result{}, err
	}
	return sdk.OneContribution(sdk.Contribution{
		Kind: sdk.ContributionNamedInterface,
		NamedInterface: &sdk.NamedInterfaceContribution{
			Name: name,
		},
	})
}
