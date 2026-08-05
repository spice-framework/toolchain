package event

import (
	"context"

	"github.com/spice-framework/spice/annotation/coretool"
	"github.com/spice-framework/spice/annotation/sdk"
)

// Listener marks a provider-owned method as a typed event listener.
//
// Spice matches the method payload to an exact topic payload type and orders
// listeners by order, module, and symbol identity. The generated invocation
// propagates context and errors without a global event bus.
//
//	// @import { Listener } from "github.com/spice-framework/spice/annotation/event"
//	// @Listener(order=10)
func Listener() sdk.Definition {
	return sdk.Definition{
		Name:    "event.Listener",
		Summary: "Declares an ordered typed application event listener.",
		Targets: []sdk.Target{sdk.TargetMethod},
		Arguments: []sdk.Argument{{
			Name:        "order",
			Kinds:       []sdk.Kind{sdk.KindInteger},
			Description: "Stable listener order; lower values run first.",
			Default:     "0",
		}},
		Examples: []sdk.Example{{
			Title: "Ordered listener",
			Code:  "// @Listener(order=10)",
		}},
		Compatibility: sdk.Compatibility{
			Since:        "0.1.0",
			MinimumSpice: "0.1.0",
		},
		Implementation: sdk.Implementation{
			Tool:     coretool.Path,
			Handler:  EventListenerHandler,
			Protocol: sdk.ProtocolV1Alpha2,
		},
	}
}

// EventListenerHandler contributes one ordered typed event listener.
func EventListenerHandler(
	_ context.Context,
	invocation sdk.Invocation,
) (sdk.Result, error) {
	if err := invocation.RequireDescriptor(
		"github.com/spice-framework/spice/annotation/event",
		"Listener",
	); err != nil {
		return sdk.Result{}, err
	}
	arguments, err := sdk.BindArguments(invocation, "", "order")
	if err != nil {
		return sdk.Result{}, err
	}
	order, err := arguments.Integer("order")
	if err != nil {
		return sdk.Result{}, err
	}
	return sdk.OneContribution(sdk.Contribution{
		Kind: sdk.ContributionEventListener,
		EventListener: &sdk.EventListenerContribution{
			Order: order,
		},
	})
}
