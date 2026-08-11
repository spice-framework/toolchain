package core

import (
	"context"

	"github.com/spice-framework/spice/annotation/coretool"
	"github.com/spice-framework/spice/annotation/sdk"
)

// Fallback makes a bean eligible only when no non-fallback bean matches the
// requested exact type and qualifiers.
func Fallback() sdk.Definition {
	return sdk.Definition{
		Name:    "core.Fallback",
		Summary: "Selects a bean only when no regular candidate matches.",
		Targets: []sdk.Target{sdk.TargetType, sdk.TargetFunction, sdk.TargetMethod},
		Examples: []sdk.Example{{
			Title: "Fallback bean",
			Code:  "// @Fallback\ntype OfflineProcessor struct{}",
		}},
		Compatibility: sdk.Compatibility{
			Since:        "0.3.0",
			MinimumSpice: "0.3.0",
		},
		Implementation: sdk.Implementation{
			Tool:     coretool.Path,
			Handler:  FallbackHandler,
			Protocol: sdk.ProtocolV1Alpha2,
		},
	}
}

// FallbackHandler contributes fallback-selection metadata.
func FallbackHandler(
	_ context.Context,
	invocation sdk.Invocation,
) (sdk.Result, error) {
	return markerMetadata(
		invocation,
		"Fallback",
		sdk.BeanMetadataContribution{Fallback: true},
	)
}
