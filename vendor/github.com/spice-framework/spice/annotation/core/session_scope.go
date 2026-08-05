package core

import (
	"context"

	"github.com/spice-framework/spice/annotation/coretool"
	"github.com/spice-framework/spice/annotation/sdk"
)

// SessionScope constructs one bean per explicit session scope and assigns its
// cleanup to that scope.
func SessionScope() sdk.Definition {
	return sdk.Definition{
		Name:    "core.SessionScope",
		Summary: "Assigns explicit session-owned bean scope.",
		Targets: []sdk.Target{sdk.TargetType, sdk.TargetFunction},
		Examples: []sdk.Example{{
			Title: "Session-owned bean",
			Code:  "// @SessionScope\ntype SessionCart struct{}",
		}},
		Compatibility: sdk.Compatibility{
			Since:        "0.3.0",
			MinimumSpice: "0.3.0",
		},
		Implementation: sdk.Implementation{
			Tool:     coretool.Path,
			Handler:  SessionScopeHandler,
			Protocol: sdk.ProtocolV1Alpha2,
		},
	}
}

// SessionScopeHandler contributes session-owned scope metadata.
func SessionScopeHandler(
	_ context.Context,
	invocation sdk.Invocation,
) (sdk.Result, error) {
	return scopeMetadata(
		invocation,
		"SessionScope",
		sdk.BeanScopeSession,
	)
}
