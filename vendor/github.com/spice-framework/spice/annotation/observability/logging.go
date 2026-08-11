// Package observability defines canonical descriptors for generated
// observability features.
package observability

import (
	"context"

	"github.com/spice-framework/spice/annotation/coretool"
	"github.com/spice-framework/spice/annotation/sdk"
)

// Logging enables Spice-native structured application logging.
//
// Generated observations use a selected injectable logging.Logger, preserve
// compiler-owned scope metadata, and cover every active Spice observation
// seam. No global logger is installed.
//
//	// @import { Logging } from "github.com/spice-framework/spice/annotation/observability"
//	// @Logging
func Logging() sdk.Definition {
	return sdk.Definition{
		Name:    "observability.Logging",
		Summary: "Enables injectable Spice-native structured logging.",
		Targets: []sdk.Target{sdk.TargetFunction},
		Examples: []sdk.Example{{
			Title: "Structured logging",
			Code:  "// @Logging",
		}},
		Compatibility: sdk.Compatibility{
			Since:        "0.1.0",
			MinimumSpice: "0.1.0",
		},
		Implementation: sdk.Implementation{
			Tool:     coretool.Path,
			Handler:  ObservabilityLoggingHandler,
			Protocol: sdk.ProtocolV1Alpha2,
		},
	}
}

// ObservabilityLoggingHandler contributes Spice-native structured logging.
func ObservabilityLoggingHandler(
	_ context.Context,
	invocation sdk.Invocation,
) (sdk.Result, error) {
	if err := invocation.RequireDescriptor(
		"github.com/spice-framework/spice/annotation/observability",
		"Logging",
	); err != nil {
		return sdk.Result{}, err
	}
	if _, err := sdk.BindArguments(invocation, ""); err != nil {
		return sdk.Result{}, err
	}
	return sdk.OneContribution(sdk.Contribution{
		Kind: sdk.ContributionBootstrap,
		Bootstrap: &sdk.BootstrapContribution{
			Capability: "observability.logging",
		},
	})
}
