// Package web defines canonical descriptors for generated net/http adapters.
package web

import (
	"context"

	"github.com/spice-framework/spice/annotation/coretool"
	"github.com/spice-framework/spice/annotation/sdk"
)

// Controller declares a constructible bean whose annotated methods are exposed
// through generated net/http adapters.
//
// Spice validates and selects the controller constructor exactly as it does
// for a @Service. It then validates method signatures, request DTO bindings, response
// handling, authorization, and route conflicts before generating ordinary
// inspectable Go. Prefix is an optional absolute route path shared by the
// controller's methods.
//
//	// @import { Controller } from "github.com/spice-framework/spice/annotation/web"
//	// @Controller(prefix="/orders", constructor=NewHTTPController)
//	type HTTPController struct{}
func Controller() sdk.Definition {
	return sdk.Definition{
		Name:    "web.Controller",
		Summary: "Declares a generated net/http controller.",
		Targets: []sdk.Target{sdk.TargetType},
		Arguments: []sdk.Argument{{
			Name:        "prefix",
			Kinds:       []sdk.Kind{sdk.KindString},
			Description: "Optional absolute route prefix.",
		}, {
			Name:        "constructor",
			Kinds:       []sdk.Kind{sdk.KindIdentifier},
			Description: "Optional same-package constructor function.",
		}, {
			Name:        "name",
			Kinds:       []sdk.Kind{sdk.KindString},
			Description: "Optional unique bean name.",
		}, {
			Name:             "aliases",
			Kinds:            []sdk.Kind{sdk.KindList},
			ListElementKinds: []sdk.Kind{sdk.KindString},
			Description:      "Optional unique alternate bean names.",
		}},
		Examples: []sdk.Example{{
			Title: "Controller",
			Code:  "// @Controller(prefix=\"/orders\", constructor=NewHTTPController)\ntype HTTPController struct{}",
		}},
		Compatibility: sdk.Compatibility{
			Since:        "0.1.0",
			MinimumSpice: "0.1.0",
		},
		Implementation: sdk.Implementation{
			Tool:     coretool.Path,
			Handler:  ControllerHandler,
			Protocol: sdk.ProtocolV1Alpha2,
		},
	}
}

// ControllerHandler contributes generated net/http controller semantics.
func ControllerHandler(
	_ context.Context,
	invocation sdk.Invocation,
) (sdk.Result, error) {
	if err := invocation.RequireDescriptor(
		"github.com/spice-framework/spice/annotation/web",
		"Controller",
	); err != nil {
		return sdk.Result{}, err
	}
	arguments, err := sdk.BindArguments(
		invocation,
		"",
		"prefix",
		"constructor",
		"name",
		"aliases",
	)
	if err != nil {
		return sdk.Result{}, err
	}
	prefix, err := arguments.String("prefix", false)
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
	return sdk.Contributions(
		sdk.Contribution{
			Kind: sdk.ContributionController,
			Controller: &sdk.ControllerContribution{
				Prefix: prefix,
			},
		},
		sdk.Contribution{
			Kind: sdk.ContributionStereotype,
			Stereotype: &sdk.StereotypeContribution{
				Role:        "controller",
				Construct:   true,
				Constructor: constructor,
				Name:        name,
				Aliases:     aliases,
			},
		},
	)
}
