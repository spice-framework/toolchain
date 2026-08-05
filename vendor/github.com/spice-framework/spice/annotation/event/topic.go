// Package event defines canonical descriptors for typed application events.
package event

import (
	"context"

	"github.com/spice-framework/spice/annotation/coretool"
	"github.com/spice-framework/spice/annotation/sdk"
)

// Topic marks a provider function that declares one typed event topic.
//
// The topic's exact payload type and ownership come from the Go signature and
// application module. Generated publishing remains explicit and observable.
//
//	// @import { Topic } from "github.com/spice-framework/spice/annotation/event"
//	// @Topic
//	func OrderChangedTopic() event.Topic[OrderChanged]
func Topic() sdk.Definition {
	return sdk.Definition{
		Name:    "event.Topic",
		Summary: "Declares a typed application event topic.",
		Targets: []sdk.Target{sdk.TargetFunction},
		Examples: []sdk.Example{{
			Title: "Typed topic",
			Code:  "// @Topic\nfunc OrderChangedTopic() event.Topic[OrderChanged]",
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
