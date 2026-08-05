// Package modulith defines canonical descriptors for compile-time application
// module boundaries.
package modulith

import (
	"context"

	"github.com/spice-framework/spice/annotation/coretool"
	"github.com/spice-framework/spice/annotation/sdk"
)

// Module marks package documentation as an application-module root.
//
// The root package is the module's default public API. Descendant packages are
// internal unless explicitly exposed by NamedInterface. Spice validates
// cross-module import edges, allowed dependencies, cycles, and unassigned
// packages before generation.
//
//	// @import { Module } from "github.com/spice-framework/spice/annotation/modulith"
//	// @Module(allowedDependencies=["example.com/app/inventory"])
//	package orders
func Module() sdk.Definition {
	return sdk.Definition{
		Name:    "modulith.Module",
		Summary: "Declares a compile-time application-module root.",
		Targets: []sdk.Target{sdk.TargetPackage},
		Arguments: []sdk.Argument{{
			Name:             "allowedDependencies",
			Kinds:            []sdk.Kind{sdk.KindList},
			ListElementKinds: []sdk.Kind{sdk.KindString},
			Description:      "Explicit module import paths or named APIs this module may use.",
		}},
		Examples: []sdk.Example{{
			Title: "Module root",
			Code:  "// @Module(allowedDependencies=[\"example.com/app/inventory\"])\npackage orders",
		}},
		Compatibility: sdk.Compatibility{
			Since:        "0.1.0",
			MinimumSpice: "0.1.0",
		},
		Implementation: sdk.Implementation{
			Tool:     coretool.Path,
			Handler:  ModuleHandler,
			Protocol: sdk.ProtocolV1Alpha2,
		},
	}
}

// ModuleHandler contributes an application-module root and its allowed
// dependencies.
func ModuleHandler(
	_ context.Context,
	invocation sdk.Invocation,
) (sdk.Result, error) {
	if err := invocation.RequireDescriptor(
		"github.com/spice-framework/spice/annotation/modulith",
		"Module",
	); err != nil {
		return sdk.Result{}, err
	}
	arguments, err := sdk.BindArguments(
		invocation,
		"",
		"allowedDependencies",
	)
	if err != nil {
		return sdk.Result{}, err
	}
	allowed, err := arguments.Strings("allowedDependencies")
	if err != nil {
		return sdk.Result{}, err
	}
	return sdk.OneContribution(sdk.Contribution{
		Kind: sdk.ContributionModule,
		Module: &sdk.ModuleContribution{
			AllowedDependencies: allowed,
		},
	})
}
