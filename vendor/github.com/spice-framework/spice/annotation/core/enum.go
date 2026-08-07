package core

import (
	"context"

	"github.com/spice-framework/spice/annotation/coretool"
	"github.com/spice-framework/spice/annotation/sdk"
)

// Enum declares that same-file constants form the complete legal value set
// of one named scalar type.
//
// Spice validates enum structure and emits ordinary type-associated parsing,
// string, and validity helpers. It does not build a runtime reflection registry.
//
//	// @import { Enum } from "github.com/spice-framework/spice/annotation/core"
//	// @Enum
//	type OrderStatus string
//
//	const (
//		OrderStatusPending   OrderStatus = "pending"
//		OrderStatusCompleted OrderStatus = "completed"
//	)
func Enum() sdk.Definition {
	return sdk.Definition{
		Name:    "core.Enum",
		Summary: "Declares a closed set of same-file typed constants.",
		Targets: []sdk.Target{sdk.TargetType},
		Examples: []sdk.Example{{
			Title: "String enum",
			Code:  "// @Enum\ntype OrderStatus string\n\nconst (\n\tOrderStatusPending OrderStatus = \"pending\"\n)",
		}},
		Compatibility: sdk.Compatibility{
			Since:        "0.2.0",
			MinimumSpice: "0.2.0",
		},
		Implementation: sdk.Implementation{
			Tool:     coretool.Path,
			Handler:  EnumHandler,
			Protocol: sdk.ProtocolV1Alpha2,
		},
	}
}

// EnumHandler contributes closed-enum validation and generation intent.
func EnumHandler(
	_ context.Context,
	invocation sdk.Invocation,
) (sdk.Result, error) {
	if err := invocation.RequireDescriptor(
		"github.com/spice-framework/spice/annotation/core",
		"Enum",
	); err != nil {
		return sdk.Result{}, err
	}
	if _, err := sdk.BindArguments(invocation, ""); err != nil {
		return sdk.Result{}, err
	}
	return sdk.OneContribution(sdk.Contribution{
		Kind: sdk.ContributionEnum,
		Enum: &sdk.EnumContribution{},
	})
}
