package core

import (
	"context"

	"github.com/spice-framework/spice/annotation/coretool"
	"github.com/spice-framework/spice/annotation/sdk"
)

// Primary makes a bean the preferred candidate when multiple non-fallback
// beans match one exact dependency type.
func Primary() sdk.Definition {
	return sdk.Definition{
		Name:    "core.Primary",
		Summary: "Selects the preferred matching bean.",
		Targets: []sdk.Target{sdk.TargetType, sdk.TargetFunction, sdk.TargetMethod},
		Examples: []sdk.Example{{
			Title: "Preferred bean",
			Code:  "// @Primary\ntype StripeProcessor struct{}",
		}},
		Compatibility: sdk.Compatibility{
			Since:        "0.3.0",
			MinimumSpice: "0.3.0",
		},
		Implementation: sdk.Implementation{
			Tool:     coretool.Path,
			Handler:  PrimaryHandler,
			Protocol: sdk.ProtocolV1Alpha2,
		},
	}
}

// PrimaryHandler contributes primary-selection metadata.
func PrimaryHandler(
	_ context.Context,
	invocation sdk.Invocation,
) (sdk.Result, error) {
	return markerMetadata(
		invocation,
		"Primary",
		sdk.BeanMetadataContribution{Primary: true},
	)
}
