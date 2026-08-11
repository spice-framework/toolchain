// Package management defines canonical descriptors for generated management
// endpoints.
package management

import (
	"context"
	"errors"

	"github.com/spice-framework/spice/annotation/coretool"
	"github.com/spice-framework/spice/annotation/sdk"
)

// Enable selects the management endpoints generated for one application.
//
// Exposure is explicit and allowlisted. Sensitive values such as
// configuration secrets are redacted. Management routes participate in the
// same module ownership, authorization, metrics, and graceful shutdown model
// as application routes.
//
//	// @import { Enable } from "github.com/spice-framework/spice/annotation/management"
//	// @Enable(expose=["health", "liveness", "readiness", "info"], access="loopback")
func Enable() sdk.Definition {
	return sdk.Definition{
		Name:    "management.Enable",
		Summary: "Selects generated management endpoints for an application.",
		Targets: []sdk.Target{sdk.TargetFunction},
		Arguments: []sdk.Argument{
			{
				Name:             "expose",
				Kinds:            []sdk.Kind{sdk.KindList},
				ListElementKinds: []sdk.Kind{sdk.KindString},
				AllowedValues:    []string{"health", "liveness", "readiness", "info", "metrics", "configprops", "modules", "loggers"},
				Description:      "Explicit management endpoint identifiers to expose.",
				Required:         true,
			},
			{
				Name:          "access",
				Kinds:         []sdk.Kind{sdk.KindString},
				AllowedValues: []string{"public", "loopback"},
				Description:   "Direct network origins allowed to use management endpoints.",
				Default:       "loopback",
			},
		},
		Examples: []sdk.Example{{
			Title: "Health endpoints",
			Code:  "// @Enable(expose=[\"health\", \"liveness\", \"readiness\"])",
		}},
		Compatibility: sdk.Compatibility{
			Since:        "0.1.0",
			MinimumSpice: "0.1.0",
		},
		Implementation: sdk.Implementation{
			Tool:     coretool.Path,
			Handler:  ManagementEnableHandler,
			Protocol: sdk.ProtocolV1Alpha2,
		},
	}
}

// ManagementEnableHandler contributes selected generated management endpoints.
func ManagementEnableHandler(
	_ context.Context,
	invocation sdk.Invocation,
) (sdk.Result, error) {
	if err := invocation.RequireDescriptor(
		"github.com/spice-framework/spice/annotation/management",
		"Enable",
	); err != nil {
		return sdk.Result{}, err
	}
	arguments, err := sdk.BindArguments(invocation, "", "expose", "access")
	if err != nil {
		return sdk.Result{}, err
	}
	if _, found := arguments["expose"]; !found {
		return sdk.Result{}, errors.New(
			"annotation argument \"expose\" is required",
		)
	}
	expose, err := arguments.Strings("expose")
	if err != nil {
		return sdk.Result{}, err
	}
	values := make([]sdk.ContributionValue, len(expose))
	for index, endpoint := range expose {
		values[index] = sdk.ContributionValue{
			Kind:   sdk.KindString,
			String: endpoint,
		}
	}
	options := []sdk.BootstrapOption{{
		Name: "expose",
		Value: sdk.ContributionValue{
			Kind: sdk.KindList,
			List: values,
		},
	}}
	access, err := arguments.String("access", false)
	if err != nil {
		return sdk.Result{}, err
	}
	if access != "" {
		options = append(options, sdk.BootstrapOption{
			Name: "access",
			Value: sdk.ContributionValue{
				Kind:   sdk.KindString,
				String: access,
			},
		})
	}
	return sdk.OneContribution(sdk.Contribution{
		Kind: sdk.ContributionBootstrap,
		Bootstrap: &sdk.BootstrapContribution{
			Capability: "management",
			Options:    options,
		},
	})
}
