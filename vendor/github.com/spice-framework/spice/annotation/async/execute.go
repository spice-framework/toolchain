// Package async defines the canonical descriptor for generated asynchronous
// method execution.
package async

import (
	"context"

	"github.com/spice-framework/spice/annotation/coretool"
	"github.com/spice-framework/spice/annotation/sdk"
)

// Execute marks a provider-owned method for generated asynchronous execution.
//
// Spice validates the exact receiver, context, request, and error signature.
// Generated code owns cancellation, panic containment, bounded execution, and
// observability; the annotation never creates a hidden global worker pool.
//
//	// @import { Execute } from "github.com/spice-framework/spice/annotation/async"
//	// @Execute
//	func (*Mailer) Deliver(context.Context, Message) error
func Execute() sdk.Definition {
	return sdk.Definition{
		Name:    "async.Execute",
		Summary: "Declares a generated asynchronous method boundary.",
		Targets: []sdk.Target{sdk.TargetMethod},
		Examples: []sdk.Example{{
			Title: "Asynchronous method",
			Code:  "// @Execute\nfunc (*Mailer) Deliver(context.Context, Message) error",
		}},
		Compatibility: sdk.Compatibility{
			Since:        "0.1.0",
			MinimumSpice: "0.1.0",
		},
		Implementation: sdk.Implementation{
			Tool:     coretool.Path,
			Handler:  AsyncExecuteHandler,
			Protocol: sdk.ProtocolV1Alpha2,
		},
	}
}

// AsyncExecuteHandler contributes generated asynchronous execution semantics.
func AsyncExecuteHandler(
	_ context.Context,
	invocation sdk.Invocation,
) (sdk.Result, error) {
	if err := invocation.RequireDescriptor(
		"github.com/spice-framework/spice/annotation/async",
		"Execute",
	); err != nil {
		return sdk.Result{}, err
	}
	if _, err := sdk.BindArguments(invocation, ""); err != nil {
		return sdk.Result{}, err
	}
	return sdk.OneContribution(sdk.Contribution{
		Kind:  sdk.ContributionAsync,
		Async: &sdk.AsyncContribution{},
	})
}
