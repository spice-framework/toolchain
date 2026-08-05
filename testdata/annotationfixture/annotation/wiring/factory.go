// Package wiring demonstrates third-party provider annotations built only
// with Spice's public annotation SDK.
package wiring

import (
	"context"

	"github.com/spice-framework/spice/annotation/sdk"
)

// Factory marks a package-level function as a compile-time provider.
//
// Spice validates the function through its ordinary exact-type provider
// contract. The fixture handler contributes provider semantics through the
// public protocol; it cannot execute the function or access compiler internals.
//
// Import Factory with a local name when that makes application code clearer:
//
//	// @import { Factory as Construct } from "example.com/spice-annotation-fixture/annotation/wiring"
//	// @Construct
//	func provideStore() *Store { return &Store{} }
func Factory() sdk.Definition {
	return sdk.Definition{
		Name:    "fixture.Factory",
		Summary: "Contributes an exact-type Spice provider.",
		Targets: []sdk.Target{sdk.TargetFunction},
		Examples: []sdk.Example{{
			Title: "Aliased provider",
			Code:  "// @Construct\nfunc provideStore() *Store { return &Store{} }",
		}},
		Compatibility: sdk.Compatibility{
			Since:        "0.1.0",
			MinimumSpice: "0.1.0",
		},
		Implementation: sdk.Implementation{
			Tool:     "example.com/spice-annotation-fixture/cmd/spice-annotations",
			Handler:  FactoryHandler,
			Protocol: sdk.ProtocolV1Alpha2,
		},
	}
}

// FactoryHandler contributes exact provider semantics for Factory.
func FactoryHandler(
	_ context.Context,
	invocation sdk.Invocation,
) (sdk.Result, error) {
	if err := invocation.RequireDescriptor(
		"example.com/spice-annotation-fixture/annotation/wiring",
		"Factory",
	); err != nil {
		return sdk.Result{}, err
	}
	return sdk.OneContribution(sdk.Contribution{
		Kind:     sdk.ContributionProvider,
		Provider: &sdk.ProviderContribution{},
	})
}
