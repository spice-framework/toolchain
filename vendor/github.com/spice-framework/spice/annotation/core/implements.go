package core

import (
	"context"

	"github.com/spice-framework/spice/annotation/coretool"
	"github.com/spice-framework/spice/annotation/sdk"
)

// Implements explicitly exposes a concrete Spice bean through one or more
// named Go interfaces.
//
// Every argument is a typed Go interface expression resolved against the
// annotation's physical source file. Spice verifies the factory's exact result
// type in the compiler's shared go/types universe and emits the equivalent Go
// blank-identifier compile-time assertion in a manifest-owned source shard.
// The annotation never performs implicit assignability scanning: concrete
// injection uses the exact result type, while interface injection sees only
// explicit Implements bindings or factories that return the interface exactly.
//
//	// @import { Implements, Service } from "github.com/spice-framework/spice/annotation/core"
//	// @import * as payments from "example.com/commerce/payments"
//	// @import * as health from "example.com/commerce/health"
//	// @Service
//	// @Implements(payments.Processor, health.Checker)
//	type StripeProcessor struct{}
//
// Generated code owns both compile-time assertions, calls the bean constructor
// directly, and relies on normal Go assignment to the interface parameter. No
// reflection, string lookup, global client, or runtime package scan is
// introduced.
func Implements() sdk.Definition {
	return sdk.Definition{
		Name:    "core.Implements",
		Summary: "Explicitly binds one concrete bean to named Go interfaces.",
		Targets: []sdk.Target{sdk.TargetType, sdk.TargetFunction, sdk.TargetMethod},
		Arguments: []sdk.Argument{{
			Name:        "interfaces",
			Kinds:       []sdk.Kind{sdk.KindIdentifier},
			ValueDomain: sdk.ValueDomainGoInterface,
			Description: "One or more named Go interface type expressions.",
			Required:    true,
			Positional:  true,
			Variadic:    true,
		}},
		Examples: []sdk.Example{{
			Title: "Concrete service implementing two interfaces",
			Code:  "// @import * as payments from \"example.com/commerce/payments\"\n// @import * as health from \"example.com/commerce/health\"\n// @Service\n// @Implements(payments.Processor, health.Checker)\ntype StripeProcessor struct{}",
		}},
		Compatibility: sdk.Compatibility{
			Since:        "0.2.0",
			MinimumSpice: "0.2.0",
		},
		Implementation: sdk.Implementation{
			Tool:     coretool.Path,
			Handler:  ImplementsHandler,
			Protocol: sdk.ProtocolV1Alpha2,
		},
	}
}

// ImplementsHandler contributes explicit interface-binding expressions. The
// compiler resolves and verifies the expressions in its existing type universe.
func ImplementsHandler(
	_ context.Context,
	invocation sdk.Invocation,
) (sdk.Result, error) {
	if err := invocation.RequireDescriptor(
		"github.com/spice-framework/spice/annotation/core",
		"Implements",
	); err != nil {
		return sdk.Result{}, err
	}
	interfaces, err := sdk.PositionalIdentifiers(invocation)
	if err != nil {
		return sdk.Result{}, err
	}
	return sdk.OneContribution(sdk.Contribution{
		Kind: sdk.ContributionInterface,
		Interface: &sdk.InterfaceBindingContribution{
			Interfaces: interfaces,
		},
	})
}
