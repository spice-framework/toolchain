package observability

import (
	"context"

	"github.com/spice-framework/spice/annotation/coretool"
	"github.com/spice-framework/spice/annotation/sdk"
)

// Observed marks an interface-bound managed service method for generated,
// instance-owned duration, failure, and panic observation.
//
//	// @import { Observed } from "github.com/spice-framework/spice/annotation/observability"
//	// @Observed(name="orders.create")
func Observed() sdk.Definition {
	return sdk.Definition{
		Name:    "observability.Observed",
		Summary: "Declares a generated service-method observation boundary.",
		Targets: []sdk.Target{sdk.TargetMethod},
		Arguments: []sdk.Argument{{
			Name:        "name",
			Kinds:       []sdk.Kind{sdk.KindString},
			Description: "Optional stable observation name.",
		}},
		Examples: []sdk.Example{{
			Title: "Observed service method",
			Code:  "// @Observed(name=\"orders.create\")",
		}},
		Compatibility: sdk.Compatibility{
			Since:        "0.2.0",
			MinimumSpice: "0.2.0",
		},
		Implementation: sdk.Implementation{
			Tool:     coretool.Path,
			Handler:  ObservedHandler,
			Protocol: sdk.ProtocolV1Alpha2,
		},
	}
}

// ObservedHandler contributes one generated method observation boundary.
func ObservedHandler(
	_ context.Context,
	invocation sdk.Invocation,
) (sdk.Result, error) {
	if err := invocation.RequireDescriptor(
		"github.com/spice-framework/spice/annotation/observability",
		"Observed",
	); err != nil {
		return sdk.Result{}, err
	}
	arguments, err := sdk.BindArguments(invocation, "", "name")
	if err != nil {
		return sdk.Result{}, err
	}
	name, err := arguments.String("name", false)
	if err != nil {
		return sdk.Result{}, err
	}
	return sdk.OneContribution(sdk.Contribution{
		Kind: sdk.ContributionObservation,
		Observation: &sdk.ObservationContribution{
			Name: name,
		},
	})
}
