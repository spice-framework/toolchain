// Package schedule defines canonical descriptors for generated scheduled
// execution.
package schedule

import (
	"context"

	"github.com/spice-framework/spice/annotation/coretool"
	"github.com/spice-framework/spice/annotation/sdk"
)

// FixedDelay marks a provider-owned method for fixed-delay execution.
//
// Delay is measured after one invocation completes. Generated scheduling owns
// cancellation, panic containment, overlap prevention, graceful shutdown, and
// observations. Duration values use time.ParseDuration syntax.
//
//	// @import { FixedDelay } from "github.com/spice-framework/spice/annotation/schedule"
//	// @FixedDelay(delay="5m", initialDelay="30s")
func FixedDelay() sdk.Definition {
	return sdk.Definition{
		Name:    "schedule.FixedDelay",
		Summary: "Declares a generated fixed-delay scheduled method.",
		Targets: []sdk.Target{sdk.TargetMethod},
		Arguments: []sdk.Argument{
			{
				Name:        "delay",
				Kinds:       []sdk.Kind{sdk.KindString},
				Description: "Required positive time.ParseDuration delay.",
				Required:    true,
			},
			{
				Name:        "initialDelay",
				Kinds:       []sdk.Kind{sdk.KindString},
				Description: "Optional non-negative delay before the first run.",
				Default:     "0s",
			},
			{
				Name:        "continueOnError",
				Kinds:       []sdk.Kind{sdk.KindBoolean},
				Description: "Whether another run is scheduled after an error.",
				Default:     "false",
			},
		},
		Examples: []sdk.Example{{
			Title: "Inventory refresh",
			Code:  "// @FixedDelay(delay=\"5m\", initialDelay=\"30s\")",
		}},
		Compatibility: sdk.Compatibility{
			Since:        "0.1.0",
			MinimumSpice: "0.1.0",
		},
		Implementation: sdk.Implementation{
			Tool:     coretool.Path,
			Handler:  FixedDelayHandler,
			Protocol: sdk.ProtocolV1Alpha2,
		},
	}
}

// FixedDelayHandler contributes one generated scheduled method.
func FixedDelayHandler(
	_ context.Context,
	invocation sdk.Invocation,
) (sdk.Result, error) {
	if err := invocation.RequireDescriptor(
		"github.com/spice-framework/spice/annotation/schedule",
		"FixedDelay",
	); err != nil {
		return sdk.Result{}, err
	}
	arguments, err := sdk.BindArguments(
		invocation,
		"",
		"delay",
		"initialDelay",
		"continueOnError",
	)
	if err != nil {
		return sdk.Result{}, err
	}
	delay, err := arguments.String("delay", true)
	if err != nil {
		return sdk.Result{}, err
	}
	initialDelay, err := arguments.String("initialDelay", false)
	if err != nil {
		return sdk.Result{}, err
	}
	continueOnError, err := arguments.Boolean("continueOnError")
	if err != nil {
		return sdk.Result{}, err
	}
	return sdk.OneContribution(sdk.Contribution{
		Kind: sdk.ContributionSchedule,
		Schedule: &sdk.ScheduleContribution{
			Delay:           delay,
			InitialDelay:    initialDelay,
			ContinueOnError: continueOnError,
		},
	})
}
