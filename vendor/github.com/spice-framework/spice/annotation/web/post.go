package web

import (
	"context"

	"github.com/spice-framework/spice/annotation/coretool"
	"github.com/spice-framework/spice/annotation/sdk"
)

// Post maps a controller method to an HTTP POST route.
//
// Path is required, may be positional, and must be absolute. Request bodies
// use explicit DTO tags and bounded decoding; generated adapters return
// RFC 9457 problem responses for validation and application failures.
//
//	// @import { Post } from "github.com/spice-framework/spice/annotation/web"
//	// @Post("/orders")
//	func (*Controller) Create(context.Context, CreateRequest) (Order, error)
func Post() sdk.Definition {
	return sdk.Definition{
		Name:    "web.Post",
		Summary: "Maps a controller method to an HTTP POST route.",
		Targets: []sdk.Target{sdk.TargetMethod},
		Arguments: []sdk.Argument{{
			Name:        "path",
			Kinds:       []sdk.Kind{sdk.KindString},
			Description: "Absolute route path, including optional path variables.",
			Required:    true,
			Positional:  true,
		}},
		Examples: []sdk.Example{{
			Title: "POST route",
			Code:  "// @Post(\"/orders\")",
		}},
		Compatibility: sdk.Compatibility{
			Since:        "0.1.0",
			MinimumSpice: "0.1.0",
		},
		Implementation: sdk.Implementation{
			Tool:     coretool.Path,
			Handler:  PostHandler,
			Protocol: sdk.ProtocolV1Alpha2,
		},
	}
}

// PostHandler contributes an HTTP POST route.
func PostHandler(
	_ context.Context,
	invocation sdk.Invocation,
) (sdk.Result, error) {
	return routeResult(invocation, "Post", "POST")
}

func routeResult(
	invocation sdk.Invocation,
	symbol string,
	method string,
) (sdk.Result, error) {
	if err := invocation.RequireDescriptor(
		"github.com/spice-framework/spice/annotation/web",
		symbol,
	); err != nil {
		return sdk.Result{}, err
	}
	arguments, err := sdk.BindArguments(invocation, "path", "path")
	if err != nil {
		return sdk.Result{}, err
	}
	path, err := arguments.String("path", true)
	if err != nil {
		return sdk.Result{}, err
	}
	return sdk.OneContribution(sdk.Contribution{
		Kind: sdk.ContributionRoute,
		Route: &sdk.RouteContribution{
			Method: method,
			Path:   path,
		},
	})
}
