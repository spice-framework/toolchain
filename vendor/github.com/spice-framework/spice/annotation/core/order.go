package core

import (
	"context"

	"github.com/spice-framework/spice/annotation/coretool"
	"github.com/spice-framework/spice/annotation/sdk"
)

// Order controls deterministic collection injection ordering. Lower values
// are injected first; equal values use bean name and source identity.
func Order() sdk.Definition {
	return sdk.Definition{
		Name:    "core.Order",
		Summary: "Orders beans injected into slices and maps.",
		Targets: []sdk.Target{sdk.TargetType, sdk.TargetFunction},
		Arguments: []sdk.Argument{{
			Name:        "value",
			Kinds:       []sdk.Kind{sdk.KindInteger},
			Required:    true,
			Positional:  true,
			Description: "Signed deterministic order.",
		}},
		Examples: []sdk.Example{{
			Title: "Ordered bean",
			Code:  "// @Order(-10)\ntype FirstFilter struct{}",
		}},
		Compatibility: sdk.Compatibility{
			Since:        "0.3.0",
			MinimumSpice: "0.3.0",
		},
		Implementation: sdk.Implementation{
			Tool:     coretool.Path,
			Handler:  OrderHandler,
			Protocol: sdk.ProtocolV1Alpha2,
		},
	}
}

// OrderHandler contributes deterministic collection ordering metadata.
func OrderHandler(
	_ context.Context,
	invocation sdk.Invocation,
) (sdk.Result, error) {
	if err := invocation.RequireDescriptor(
		"github.com/spice-framework/spice/annotation/core",
		"Order",
	); err != nil {
		return sdk.Result{}, err
	}
	arguments, err := sdk.BindArguments(invocation, "value", "value")
	if err != nil {
		return sdk.Result{}, err
	}
	value, err := arguments.Integer("value")
	if err != nil {
		return sdk.Result{}, err
	}
	return sdk.OneContribution(sdk.Contribution{
		Kind: sdk.ContributionBeanMetadata,
		BeanMetadata: &sdk.BeanMetadataContribution{
			Order: &value,
		},
	})
}
