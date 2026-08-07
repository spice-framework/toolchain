// Package event defines canonical descriptors for typed application events.
package event

import (
	"context"

	"github.com/spice-framework/spice/annotation/coretool"
	"github.com/spice-framework/spice/annotation/sdk"
)

// Topic marks an event payload type as one typed event topic.
//
// The payload's exact type identity and ownership come from its Go declaration
// and application module. Spice contributes a synthetic event.Publisher[T]
// provider. Generated publishing remains explicit and observable.
//
//	// @import { Topic } from "github.com/spice-framework/spice/annotation/event"
//	// @Topic
//	type OrderChanged struct {
//		OrderID string
//	}
//
// Package-level function topics remain temporarily accepted for pre-0.2
// migration. New applications should annotate the payload type.
func Topic() sdk.Definition {
	return sdk.Definition{
		Name:    "event.Topic",
		Summary: "Declares a typed application event topic.",
		Targets: []sdk.Target{sdk.TargetType, sdk.TargetFunction},
		Examples: []sdk.Example{{
			Title: "Type-owned topic",
			Code:  "// @Topic\ntype OrderChanged struct {\n\tOrderID string\n}",
		}},
		Compatibility: sdk.Compatibility{
			Since:        "0.1.0",
			MinimumSpice: "0.1.0",
		},
		Implementation: sdk.Implementation{
			Tool:     coretool.Path,
			Handler:  EventTopicHandler,
			Protocol: sdk.ProtocolV1Alpha2,
		},
	}
}

// EventTopicHandler contributes a typed event topic provider.
func EventTopicHandler(
	_ context.Context,
	invocation sdk.Invocation,
) (sdk.Result, error) {
	if err := invocation.RequireDescriptor(
		"github.com/spice-framework/spice/annotation/event",
		"Topic",
	); err != nil {
		return sdk.Result{}, err
	}
	if _, err := sdk.BindArguments(invocation, ""); err != nil {
		return sdk.Result{}, err
	}
	return sdk.OneContribution(sdk.Contribution{
		Kind:       sdk.ContributionEventTopic,
		EventTopic: &sdk.EventTopicContribution{},
	})
}
