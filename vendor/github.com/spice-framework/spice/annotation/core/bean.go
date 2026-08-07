package core

import (
	"context"

	"github.com/spice-framework/spice/annotation/coretool"
	"github.com/spice-framework/spice/annotation/sdk"
)

// Bean marks a provider function or a method on a @Configuration type.
//
// Spice derives dependencies and output from exact Go type identity. A
// provider may additionally return lifecycle.Cleanup and error. Cleanup is
// registered immediately after construction and runs in reverse order during
// rollback or shutdown. Interface outputs require an explicit adapter
// provider; assignability is never guessed.
//
//	// @import { Bean } from "github.com/spice-framework/spice/annotation/core"
//	// @Bean
//	type StoreConfiguration struct{}
//
//	// @Bean
//	func (*StoreConfiguration) Store(config Config) (*Store, lifecycle.Cleanup, error)
func Bean() sdk.Definition {
	return sdk.Definition{
		Name:    "core.Bean",
		Summary: "Declares an exact-type dependency provider.",
		Targets: []sdk.Target{sdk.TargetFunction, sdk.TargetMethod},
		Arguments: []sdk.Argument{
			{
				Name:        "name",
				Kinds:       []sdk.Kind{sdk.KindString},
				Description: "Optional unique bean name.",
			},
			{
				Name:             "aliases",
				Kinds:            []sdk.Kind{sdk.KindList},
				ListElementKinds: []sdk.Kind{sdk.KindString},
				Description:      "Optional unique alternate bean names.",
			},
		},
		Examples: []sdk.Example{{
			Title: "Configuration method provider",
			Code:  "// @Bean\nfunc (*StoreConfiguration) Store(config Config) (*Store, error)",
		}},
		Compatibility: sdk.Compatibility{
			Since:        "0.1.0",
			MinimumSpice: "0.1.0",
		},
		Implementation: sdk.Implementation{
			Tool:     coretool.Path,
			Handler:  BeanHandler,
			Protocol: sdk.ProtocolV1Alpha2,
		},
	}
}

// BeanHandler contributes exact-type provider semantics.
func BeanHandler(
	_ context.Context,
	invocation sdk.Invocation,
) (sdk.Result, error) {
	if err := invocation.RequireDescriptor(
		"github.com/spice-framework/spice/annotation/core",
		"Bean",
	); err != nil {
		return sdk.Result{}, err
	}
	arguments, err := sdk.BindArguments(
		invocation,
		"",
		"name",
		"aliases",
	)
	if err != nil {
		return sdk.Result{}, err
	}
	name, aliases, err := sdk.BeanIdentity(arguments)
	if err != nil {
		return sdk.Result{}, err
	}
	return sdk.OneContribution(sdk.Contribution{
		Kind: sdk.ContributionProvider,
		Provider: &sdk.ProviderContribution{
			Name:    name,
			Aliases: aliases,
		},
	})
}
