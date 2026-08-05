package web

import (
	"context"

	"github.com/spice-framework/spice/annotation/coretool"
	"github.com/spice-framework/spice/annotation/sdk"
)

// Get maps a controller method to an HTTP GET route.
//
// Path is required, may be positional, and must be absolute. Spice validates
// path parameters against request DTO tags and generates content negotiation,
// validation, problem responses, and explicit response writing.
//
//	// @import { Get } from "github.com/spice-framework/spice/annotation/web"
//	// @Get("/orders/{id}")
//	func (*Controller) Find(context.Context, FindRequest) (Order, error)
func Get() sdk.Definition {
	return sdk.Definition{
		Name:    "web.Get",
		Summary: "Maps a controller method to an HTTP GET route.",
		Targets: []sdk.Target{sdk.TargetMethod},
		Arguments: []sdk.Argument{{
			Name:        "path",
			Kinds:       []sdk.Kind{sdk.KindString},
			Description: "Absolute route path, including optional path variables.",
			Required:    true,
			Positional:  true,
		}},
		Examples: []sdk.Example{{
			Title: "GET route",
			Code:  "// @Get(\"/orders/{id}\")",
		}},
		Compatibility: sdk.Compatibility{
			Since:        "0.1.0",
			MinimumSpice: "0.1.0",
		},
		Implementation: sdk.Implementation{
			Tool:     coretool.Path,
			Handler:  GetHandler,
			Protocol: sdk.ProtocolV1Alpha2,
		},
	}
}

// GetHandler contributes an HTTP GET route.
func GetHandler(
	_ context.Context,
	invocation sdk.Invocation,
) (sdk.Result, error) {
	return routeResult(invocation, "Get", "GET")
}
