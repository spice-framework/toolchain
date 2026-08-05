// Package policy demonstrates namespace-qualified third-party annotations.
package policy

import (
	"context"

	"github.com/spice-framework/spice/annotation/sdk"
)

// Policy classifies a type under a fixture-owned architecture policy.
//
// The strict mode contributes a stereotype. The deny mode intentionally emits
// a source-positioned plugin diagnostic, giving the integration fixture a
// deterministic failure path without hiding validation in the compiler.
//
// Use a namespace import to keep the annotation's owner visible:
//
//	// @import * as fixture from "example.com/spice-annotation-fixture/annotation/policy"
//	// @fixture.Policy(mode="strict")
//	type Settings struct{}
func Policy() sdk.Definition {
	return sdk.Definition{
		Name:    "fixture.Policy",
		Summary: "Applies the fixture's documented architecture policy.",
		Targets: []sdk.Target{sdk.TargetType},
		Arguments: []sdk.Argument{{
			Name:          "mode",
			Kinds:         []sdk.Kind{sdk.KindString},
			AllowedValues: []string{"strict", "deny"},
			Description:   "Selects strict classification or a deliberate diagnostic.",
			Default:       "strict",
		}},
		Examples: []sdk.Example{{
			Title: "Namespaced policy",
			Code:  "// @fixture.Policy(mode=\"strict\")\ntype Settings struct{}",
		}},
		Compatibility: sdk.Compatibility{
			Since:        "0.1.0",
			MinimumSpice: "0.1.0",
		},
		Implementation: sdk.Implementation{
			Tool:     "example.com/spice-annotation-fixture/cmd/spice-annotations",
			Handler:  PolicyHandler,
			Protocol: sdk.ProtocolV1Alpha2,
		},
	}
}

// PolicyHandler contributes fixture policy semantics and diagnostics.
func PolicyHandler(
	_ context.Context,
	invocation sdk.Invocation,
) (sdk.Result, error) {
	if err := invocation.RequireDescriptor(
		"example.com/spice-annotation-fixture/annotation/policy",
		"Policy",
	); err != nil {
		return sdk.Result{}, err
	}
	arguments, err := sdk.BindArguments(invocation, "", "mode")
	if err != nil {
		return sdk.Result{}, err
	}
	mode, err := arguments.String("mode", false)
	if err != nil {
		return sdk.Result{}, err
	}
	if mode == "deny" {
		return sdk.Result{
			Diagnostics: []sdk.HandlerDiagnostic{{
				Code:     "policy-denied",
				Severity: "error",
				Message:  "fixture policy deliberately denied this declaration",
			}},
		}, nil
	}
	return sdk.OneContribution(sdk.Contribution{
		Kind: sdk.ContributionStereotype,
		Stereotype: &sdk.StereotypeContribution{
			Role: "fixture-policy",
		},
	})
}
