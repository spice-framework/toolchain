// Package core defines the canonical descriptors for Spice's application and
// dependency-injection annotations.
package core

import (
	"context"

	"github.com/spice-framework/spice/annotation/coretool"
	"github.com/spice-framework/spice/annotation/sdk"
)

// Application marks the package-level function that defines a Spice
// application target.
//
// The function remains ordinary valid Go. Spice inspects its exact parameter
// types as application roots and never executes its body during analysis.
// Argument-free package-main markers compose same-module packages through
// explicit blank Go imports on the command package. The imported packages join
// the same typed compiler program; named imports and external side-effect
// imports retain ordinary Go semantics.
// Generated NewApplication, Start, Stop, and Run code owns construction,
// rollback, lifecycle ordering, and shutdown; no runtime reflection or global
// service locator is introduced.
//
// Use one explicit import in every file that declares the marker:
//
//	// @import { Application } from "github.com/spice-framework/spice/annotation/core"
//	// @Application
//	func main() {}
func Application() sdk.Definition {
	return sdk.Definition{
		Name:    "core.Application",
		Summary: "Defines a compile-time Spice application target.",
		Targets: []sdk.Target{sdk.TargetFunction},
		Examples: []sdk.Example{{
			Title: "Package-main application",
			Code:  "// @Application\nfunc main() {}",
		}},
		Compatibility: sdk.Compatibility{
			Since:        "0.1.0",
			MinimumSpice: "0.1.0",
		},
		Implementation: sdk.Implementation{
			Tool:     coretool.Path,
			Handler:  ApplicationHandler,
			Protocol: sdk.ProtocolV1Alpha2,
		},
	}
}

// ApplicationHandler contributes application-marker semantics.
func ApplicationHandler(
	_ context.Context,
	invocation sdk.Invocation,
) (sdk.Result, error) {
	if err := invocation.RequireDescriptor(
		"github.com/spice-framework/spice/annotation/core",
		"Application",
	); err != nil {
		return sdk.Result{}, err
	}
	return sdk.OneContribution(sdk.Contribution{
		Kind:        sdk.ContributionApplication,
		Application: &sdk.ApplicationContribution{},
	})
}
