package core

import (
	"context"

	"github.com/spice-framework/spice/annotation/coretool"
	"github.com/spice-framework/spice/annotation/sdk"
)

// Prototype constructs a fresh bean for each generated provider acquisition.
// Its cleanup is returned to the caller and is never hidden in application
// shutdown.
func Prototype() sdk.Definition {
	return sdk.Definition{
		Name:    "core.Prototype",
		Summary: "Assigns caller-owned prototype bean scope.",
		Targets: []sdk.Target{sdk.TargetType, sdk.TargetFunction, sdk.TargetMethod},
		Examples: []sdk.Example{{
			Title: "Caller-owned bean",
			Code:  "// @Prototype\ntype WorkUnit struct{}",
		}},
		Compatibility: sdk.Compatibility{
			Since:        "0.3.0",
			MinimumSpice: "0.3.0",
		},
		Implementation: sdk.Implementation{
			Tool:     coretool.Path,
			Handler:  PrototypeHandler,
			Protocol: sdk.ProtocolV1Alpha2,
		},
	}
}

// PrototypeHandler contributes caller-owned prototype scope metadata.
func PrototypeHandler(
	_ context.Context,
	invocation sdk.Invocation,
) (sdk.Result, error) {
	return scopeMetadata(invocation, "Prototype", sdk.BeanScopePrototype)
}
