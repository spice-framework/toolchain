package core

import (
	"context"

	"github.com/spice-framework/spice/annotation/coretool"
	"github.com/spice-framework/spice/annotation/sdk"
)

// Service declares a constructible application-service bean.
//
// Spice selects an ordinary Go constructor at compile time: an explicit
// constructor symbol, New<Type>, the unambiguous package New function, or
// generated new(T). Dependencies remain constructor parameters and generated
// code calls the selected constructor directly. No reflection or service
// locator is used. Use @Implements to expose the concrete result through an
// interface; Spice verifies it with a generated Go compile-time assertion.
//
//	// @import { Service } from "github.com/spice-framework/spice/annotation/core"
//	// @Service(constructor=NewOrders)
//	type Orders struct{}
//
//	func NewOrders(repository Repository) *Orders
func Service() sdk.Definition {
	return sdk.Definition{
		Name:    "core.Service",
		Summary: "Declares a constructible application-service bean.",
		Targets: []sdk.Target{sdk.TargetType},
		Arguments: []sdk.Argument{
			{
				Name:        "constructor",
				Kinds:       []sdk.Kind{sdk.KindIdentifier},
				Description: "Optional same-package constructor function.",
			},
			{
				Name:        "name",
				Kinds:       []sdk.Kind{sdk.KindString},
				Description: "Optional unique bean name.",
			},
			{
				Name:             "aliases",
				Kinds:            []sdk.Kind{sdk.KindList},
				ListElementKinds: []sdk.Kind{sdk.KindString},
				Description:      "Optional unique alternate bean names.",
			},
		},
		Examples: []sdk.Example{{
			Title: "Service with explicit constructor",
			Code:  "// @Service(constructor=NewOrders)\ntype Orders struct{}\n\nfunc NewOrders(repository Repository) *Orders",
		}},
		Compatibility: sdk.Compatibility{
			Since:        "0.1.0",
			MinimumSpice: "0.1.0",
		},
		Implementation: sdk.Implementation{
			Tool:     coretool.Path,
			Handler:  ServiceHandler,
			Protocol: sdk.ProtocolV1Alpha2,
		},
	}
}

// ServiceHandler contributes explicit service stereotype semantics.
func ServiceHandler(
	_ context.Context,
	invocation sdk.Invocation,
) (sdk.Result, error) {
	if err := invocation.RequireDescriptor(
		"github.com/spice-framework/spice/annotation/core",
		"Service",
	); err != nil {
		return sdk.Result{}, err
	}
	arguments, err := sdk.BindArguments(
		invocation,
		"",
		"constructor",
		"name",
		"aliases",
	)
	if err != nil {
		return sdk.Result{}, err
	}
	constructor, err := arguments.Identifier("constructor", false)
	if err != nil {
		return sdk.Result{}, err
	}
	name, aliases, err := sdk.BeanIdentity(arguments)
	if err != nil {
		return sdk.Result{}, err
	}
	return sdk.OneContribution(sdk.Contribution{
		Kind: sdk.ContributionStereotype,
		Stereotype: &sdk.StereotypeContribution{
			Role:        "service",
			Construct:   true,
			Constructor: constructor,
			Name:        name,
			Aliases:     aliases,
		},
	})
}
