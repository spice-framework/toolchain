package core

import (
	"context"

	"github.com/spice-framework/spice/annotation/coretool"
	"github.com/spice-framework/spice/annotation/sdk"
)

// Qualifier assigns a semantic selection name to a bean or requests that
// qualifier on one constructor parameter.
//
// Qualifiers are explicit compile-time metadata. They never trigger runtime
// string lookup: the compiler resolves the selected declaration and generated
// Go passes its typed value directly.
//
//	// @Qualifier("stripe")
//	type StripeProcessor struct{}
//
//	func NewCheckout(
//		// @Qualifier("stripe")
//		processor payments.Processor,
//	) *Checkout
func Qualifier() sdk.Definition {
	return sdk.Definition{
		Name:       "core.Qualifier",
		Summary:    "Names a bean candidate or constructor dependency.",
		Targets:    []sdk.Target{sdk.TargetType, sdk.TargetFunction, sdk.TargetMethod, sdk.TargetParameter},
		Repeatable: true,
		Arguments: []sdk.Argument{{
			Name:        "value",
			Kinds:       []sdk.Kind{sdk.KindString},
			Required:    true,
			Positional:  true,
			Description: "Required selection qualifier.",
		}},
		Examples: []sdk.Example{{
			Title: "Qualified bean",
			Code:  "// @Qualifier(\"stripe\")\ntype StripeProcessor struct{}",
		}},
		Compatibility: sdk.Compatibility{
			Since:        "0.3.0",
			MinimumSpice: "0.3.0",
		},
		Implementation: sdk.Implementation{
			Tool:     coretool.Path,
			Handler:  QualifierHandler,
			Protocol: sdk.ProtocolV1Alpha2,
		},
	}
}

// QualifierHandler contributes one deterministic selection qualifier.
func QualifierHandler(
	_ context.Context,
	invocation sdk.Invocation,
) (sdk.Result, error) {
	if err := invocation.RequireDescriptor(
		"github.com/spice-framework/spice/annotation/core",
		"Qualifier",
	); err != nil {
		return sdk.Result{}, err
	}
	arguments, err := sdk.BindArguments(invocation, "value", "value")
	if err != nil {
		return sdk.Result{}, err
	}
	value, err := arguments.String("value", true)
	if err != nil {
		return sdk.Result{}, err
	}
	return sdk.OneContribution(sdk.Contribution{
		Kind: sdk.ContributionBeanMetadata,
		BeanMetadata: &sdk.BeanMetadataContribution{
			Qualifiers: []string{value},
		},
	})
}
