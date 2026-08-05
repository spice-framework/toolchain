package lifecycle

import (
	"context"

	"github.com/spice-framework/spice/annotation/coretool"
	"github.com/spice-framework/spice/annotation/sdk"
)

// OnStop marks a provider-owned method that runs during graceful shutdown.
//
// The exact method signature is func(context.Context) error. Callbacks run in
// reverse dependency order before construction cleanups. Stop is idempotent
// and uses the caller-owned shutdown context.
//
//	// @import { OnStop } from "github.com/spice-framework/spice/annotation/lifecycle"
//	// @OnStop
//	func (*Server) Stop(context.Context) error
func OnStop() sdk.Definition {
	return sdk.Definition{
		Name:    "lifecycle.OnStop",
		Summary: "Declares a reverse-dependency application stop callback.",
		Targets: []sdk.Target{sdk.TargetMethod},
		Examples: []sdk.Example{{
			Title: "Stop callback",
			Code:  "// @OnStop\nfunc (*Server) Stop(context.Context) error",
		}},
		Compatibility: sdk.Compatibility{
			Since:        "0.1.0",
			MinimumSpice: "0.1.0",
		},
		Implementation: sdk.Implementation{
			Tool:     coretool.Path,
			Handler:  OnStopHandler,
			Protocol: sdk.ProtocolV1Alpha2,
		},
	}
}

// OnStopHandler contributes a reverse-dependency stop callback.
func OnStopHandler(
	_ context.Context,
	invocation sdk.Invocation,
) (sdk.Result, error) {
	return lifecycleResult(invocation, "OnStop", sdk.LifecycleStop)
}

func lifecycleResult(
	invocation sdk.Invocation,
	symbol string,
	phase sdk.LifecyclePhase,
) (sdk.Result, error) {
	if err := invocation.RequireDescriptor(
		"github.com/spice-framework/spice/annotation/lifecycle",
		symbol,
	); err != nil {
		return sdk.Result{}, err
	}
	if _, err := sdk.BindArguments(invocation, ""); err != nil {
		return sdk.Result{}, err
	}
	return sdk.OneContribution(sdk.Contribution{
		Kind: sdk.ContributionLifecycle,
		Lifecycle: &sdk.LifecycleContribution{
			Phase: phase,
		},
	})
}
