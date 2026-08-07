// Package cache defines the canonical descriptor for generated cache
// boundaries.
package cache

import (
	"context"

	"github.com/spice-framework/spice/annotation/coretool"
	"github.com/spice-framework/spice/annotation/sdk"
)

// Cacheable marks a typed GET controller method or an interface-bound managed
// service method whose successful response may be cached under a stable name.
//
// Spice generates deterministic key material from the validated request and
// keeps the cache implementation explicit through dependency injection.
// Errors and unauthorized responses are never cached.
//
//	// @import { Cacheable } from "github.com/spice-framework/spice/annotation/cache"
//	// @Cacheable(name="orders.by-id")
func Cacheable() sdk.Definition {
	return sdk.Definition{
		Name:    "cache.Cacheable",
		Summary: "Declares a named generated typed cache boundary.",
		Targets: []sdk.Target{sdk.TargetMethod},
		Arguments: []sdk.Argument{{
			Name:        "name",
			Kinds:       []sdk.Kind{sdk.KindString},
			Description: "Stable cache identity used for configuration and observations.",
			Required:    true,
		}},
		Examples: []sdk.Example{{
			Title: "Cached typed method",
			Code:  "// @Cacheable(name=\"orders.by-id\")",
		}},
		Compatibility: sdk.Compatibility{
			Since:        "0.1.0",
			MinimumSpice: "0.1.0",
		},
		Implementation: sdk.Implementation{
			Tool:     coretool.Path,
			Handler:  CacheableHandler,
			Protocol: sdk.ProtocolV1Alpha2,
		},
	}
}

// CacheableHandler contributes one named generated cache boundary.
func CacheableHandler(
	_ context.Context,
	invocation sdk.Invocation,
) (sdk.Result, error) {
	if err := invocation.RequireDescriptor(
		"github.com/spice-framework/spice/annotation/cache",
		"Cacheable",
	); err != nil {
		return sdk.Result{}, err
	}
	arguments, err := sdk.BindArguments(invocation, "", "name")
	if err != nil {
		return sdk.Result{}, err
	}
	name, err := arguments.String("name", true)
	if err != nil {
		return sdk.Result{}, err
	}
	return sdk.OneContribution(sdk.Contribution{
		Kind: sdk.ContributionCache,
		Cache: &sdk.CacheContribution{
			Name: name,
		},
	})
}
