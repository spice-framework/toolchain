package core

import (
	"context"

	"github.com/spice-framework/spice/annotation/coretool"
	"github.com/spice-framework/spice/annotation/sdk"
)

// Singleton assigns the default application-owned scope explicitly.
func Singleton() sdk.Definition {
	return sdk.Definition{
		Name:    "core.Singleton",
		Summary: "Assigns application-owned singleton bean scope.",
		Targets: []sdk.Target{sdk.TargetType, sdk.TargetFunction},
		Examples: []sdk.Example{{
			Title: "Application-owned bean",
			Code:  "// @Singleton\ntype Catalog struct{}",
		}},
		Compatibility: sdk.Compatibility{
			Since:        "0.3.0",
			MinimumSpice: "0.3.0",
		},
		Implementation: sdk.Implementation{
			Tool:     coretool.Path,
			Handler:  SingletonHandler,
			Protocol: sdk.ProtocolV1Alpha2,
		},
	}
}

// SingletonHandler contributes application-owned singleton scope metadata.
func SingletonHandler(
	_ context.Context,
	invocation sdk.Invocation,
) (sdk.Result, error) {
	return scopeMetadata(invocation, "Singleton", sdk.BeanScopeSingleton)
}
