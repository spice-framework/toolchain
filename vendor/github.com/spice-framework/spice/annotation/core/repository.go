package core

import (
	"context"

	"github.com/spice-framework/spice/annotation/coretool"
	"github.com/spice-framework/spice/annotation/sdk"
)

// Repository declares a constructible data-access bean.
//
// Constructor discovery and dependency injection follow the same compile-time
// rules as Service. Repository is a semantic role for module ownership,
// transactions, diagnostics, navigation, and observability; generated code
// still contains only direct ordinary Go constructor calls.
//
//	// @import { Repository } from "github.com/spice-framework/spice/annotation/core"
//	// @Repository(constructor=NewOrderRepository)
//	type OrderRepository struct{}
func Repository() sdk.Definition {
	return sdk.Definition{
		Name:    "core.Repository",
		Summary: "Declares a constructible data-access bean.",
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
			Title: "Repository with constructor discovery",
			Code:  "// @Repository\ntype OrderRepository struct{}\n\nfunc NewOrderRepository(database *sql.DB) *OrderRepository",
		}},
		Compatibility: sdk.Compatibility{
			Since:        "0.2.0",
			MinimumSpice: "0.2.0",
		},
		Implementation: sdk.Implementation{
			Tool:     coretool.Path,
			Handler:  RepositoryHandler,
			Protocol: sdk.ProtocolV1Alpha2,
		},
	}
}

// RepositoryHandler contributes explicit repository construction metadata.
func RepositoryHandler(
	_ context.Context,
	invocation sdk.Invocation,
) (sdk.Result, error) {
	if err := invocation.RequireDescriptor(
		"github.com/spice-framework/spice/annotation/core",
		"Repository",
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
			Role:        "repository",
			Construct:   true,
			Constructor: constructor,
			Name:        name,
			Aliases:     aliases,
		},
	})
}
