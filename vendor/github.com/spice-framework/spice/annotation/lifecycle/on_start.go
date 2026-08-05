// Package lifecycle defines canonical descriptors for generated application
// lifecycle callbacks.
package lifecycle

import (
	"context"

	"github.com/spice-framework/spice/annotation/coretool"
	"github.com/spice-framework/spice/annotation/sdk"
)

// OnStart marks a provider-owned method that runs after dependency
// construction and before the application becomes ready.
//
// The exact method signature is func(context.Context) error. Callbacks start
// dependency-first. A failure rolls back already-started callbacks and
// constructed providers in reverse deterministic order.
//
//	// @import { OnStart } from "github.com/spice-framework/spice/annotation/lifecycle"
//	// @OnStart
//	func (*Server) Start(context.Context) error
func OnStart() sdk.Definition {
	return sdk.Definition{
		Name:    "lifecycle.OnStart",
		Summary: "Declares a dependency-ordered application start callback.",
		Targets: []sdk.Target{sdk.TargetMethod},
		Examples: []sdk.Example{{
			Title: "Start callback",
			Code:  "// @OnStart\nfunc (*Server) Start(context.Context) error",
		}},
		Compatibility: sdk.Compatibility{
			Since:        "0.1.0",
			MinimumSpice: "0.1.0",
		},
		Implementation: sdk.Implementation{
			Tool:     coretool.Path,
			Handler:  OnStartHandler,
			Protocol: sdk.ProtocolV1Alpha2,
		},
	}
}

// OnStartHandler contributes a dependency-ordered start callback.
func OnStartHandler(
	_ context.Context,
	invocation sdk.Invocation,
) (sdk.Result, error) {
	return lifecycleResult(invocation, "OnStart", sdk.LifecycleStart)
}
