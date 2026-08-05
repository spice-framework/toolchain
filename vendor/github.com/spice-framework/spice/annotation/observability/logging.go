// Package observability defines canonical descriptors for generated
// observability features.
package observability

import (
	"context"

	"github.com/spice-framework/spice/annotation/coretool"
	"github.com/spice-framework/spice/annotation/sdk"
)

// Logging enables structured application and module lifecycle logging.
//
// Generated observations use slog-compatible structured records and preserve
// application, module, component, operation, and phase fields. No global
// logger is installed; the logger remains an explicit dependency.
//
//	// @import { Logging } from "github.com/spice-framework/spice/annotation/observability"
//	// @Logging
func Logging() sdk.Definition {
	return sdk.Definition{
		Name:    "observability.Logging",
		Summary: "Enables generated structured lifecycle logging.",
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

// ObservabilityLoggingHandler contributes structured lifecycle logging.
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
