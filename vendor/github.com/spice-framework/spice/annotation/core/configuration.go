package core

import (
	"context"

	"github.com/spice-framework/spice/annotation/coretool"
	"github.com/spice-framework/spice/annotation/sdk"
)

// Configuration marks a struct as generated typed configuration.
//
// Field metadata remains on ordinary Go struct tags. Spice derives stable
// keys, defaults, required values, secret redaction, validation, and generated
// metadata without reflecting over the application at runtime. Prefix is
// optional and must be dot-separated identifiers.
//
//	// @import { Configuration } from "github.com/spice-framework/spice/annotation/core"
//	// @Configuration(prefix="orders")
//	type Settings struct {
//		Limit int `spice:"limit,default=100"`
//	}
func Configuration() sdk.Definition {
	return sdk.Definition{
		Name:    "core.Configuration",
		Summary: "Declares a generated typed configuration struct.",
		Targets: []sdk.Target{sdk.TargetType},
		Arguments: []sdk.Argument{{
			Name:        "prefix",
			Kinds:       []sdk.Kind{sdk.KindString},
			Description: "Optional dot-separated property-key prefix.",
		}},
		Examples: []sdk.Example{{
			Title: "Configuration",
			Code:  "// @Configuration(prefix=\"orders\")\ntype Settings struct{}",
		}},
		Compatibility: sdk.Compatibility{
			Since:        "0.1.0",
			MinimumSpice: "0.1.0",
		},
		Implementation: sdk.Implementation{
			Tool:     coretool.Path,
			Handler:  ConfigurationHandler,
			Protocol: sdk.ProtocolV1Alpha2,
		},
	}
}

// ConfigurationHandler contributes typed configuration semantics.
func ConfigurationHandler(
	_ context.Context,
	invocation sdk.Invocation,
) (sdk.Result, error) {
	if err := invocation.RequireDescriptor(
		"github.com/spice-framework/spice/annotation/core",
		"Configuration",
	); err != nil {
		return sdk.Result{}, err
	}
	arguments, err := sdk.BindArguments(invocation, "", "prefix")
	if err != nil {
		return sdk.Result{}, err
	}
	prefix, err := arguments.String("prefix", false)
	if err != nil {
		return sdk.Result{}, err
	}
	return sdk.OneContribution(sdk.Contribution{
		Kind: sdk.ContributionConfiguration,
		Configuration: &sdk.ConfigurationContribution{
			Prefix: prefix,
		},
	})
}
