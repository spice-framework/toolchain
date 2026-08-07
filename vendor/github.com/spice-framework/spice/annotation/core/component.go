package core

import (
	"context"

	"github.com/spice-framework/spice/annotation/coretool"
	"github.com/spice-framework/spice/annotation/sdk"
)

// Component declares a constructible generic managed bean.
//
// Use Component when a managed type is not application-service logic, a data
// repository, an HTTP controller, or a configuration factory. Spice selects
// and calls an ordinary Go constructor directly; it does not scan packages or
// use reflection. Component supports the same bean identity, scope, ordering,
// selection, and explicit interface-binding annotations as Service.
//
//	// @import { Component } from "github.com/spice-framework/spice/annotation/core"
//	// @Component
//	type PasswordHasher struct{}
//
//	func NewPasswordHasher() *PasswordHasher
func Component() sdk.Definition {
	return sdk.Definition{
		Name:    "core.Component",
		Summary: "Declares a constructible generic managed bean.",
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
			Title: "Generic component",
			Code:  "// @Component\ntype PasswordHasher struct{}\n\nfunc NewPasswordHasher() *PasswordHasher",
		}},
		Compatibility: sdk.Compatibility{
			Since:        "0.2.0",
			MinimumSpice: "0.2.0",
		},
		Implementation: sdk.Implementation{
			Tool:     coretool.Path,
			Handler:  ComponentHandler,
			Protocol: sdk.ProtocolV1Alpha2,
		},
	}
}

// ComponentHandler contributes generic component construction metadata.
func ComponentHandler(
	_ context.Context,
	invocation sdk.Invocation,
) (sdk.Result, error) {
	if err := invocation.RequireDescriptor(
		"github.com/spice-framework/spice/annotation/core",
		"Component",
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
			Role:        "component",
			Construct:   true,
			Constructor: constructor,
			Name:        name,
			Aliases:     aliases,
		},
	})
}
