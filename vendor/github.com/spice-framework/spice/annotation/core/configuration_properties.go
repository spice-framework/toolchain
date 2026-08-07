package core

import (
	"context"

	"github.com/spice-framework/spice/annotation/coretool"
	"github.com/spice-framework/spice/annotation/sdk"
)

// ConfigurationProperties marks a struct as generated typed configuration.
//
// Field metadata remains on ordinary Go struct tags. Spice derives stable
// keys, defaults, required values, secret redaction, validation, and generated
// metadata without reflecting over the application at runtime. Prefix is
// optional and must be dot-separated identifiers.
//
//	// @import { ConfigurationProperties } from "github.com/spice-framework/spice/annotation/core"
//	// @ConfigurationProperties(prefix="orders")
//	type OrderProperties struct {
//		Limit int `spice:"limit,default=100"`
//	}
func ConfigurationProperties() sdk.Definition {
	return sdk.Definition{
		Name:    "core.ConfigurationProperties",
		Summary: "Declares generated typed configuration properties.",
		Targets: []sdk.Target{sdk.TargetType},
		Arguments: []sdk.Argument{{
			Name:        "prefix",
			Kinds:       []sdk.Kind{sdk.KindString},
			Description: "Optional dot-separated property-key prefix.",
		}},
		Examples: []sdk.Example{{
			Title: "Typed configuration properties",
			Code:  "// @ConfigurationProperties(prefix=\"orders\")\ntype OrderProperties struct{}",
		}},
		Compatibility: sdk.Compatibility{
			Since:        "0.2.0",
			MinimumSpice: "0.2.0",
		},
		Implementation: sdk.Implementation{
			Tool:     coretool.Path,
			Handler:  ConfigurationPropertiesHandler,
			Protocol: sdk.ProtocolV1Alpha2,
		},
	}
}

// ConfigurationPropertiesHandler contributes typed properties semantics.
func ConfigurationPropertiesHandler(
	_ context.Context,
	invocation sdk.Invocation,
) (sdk.Result, error) {
	if err := invocation.RequireDescriptor(
		"github.com/spice-framework/spice/annotation/core",
		"ConfigurationProperties",
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
