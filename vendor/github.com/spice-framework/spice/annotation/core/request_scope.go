package core

import (
	"context"

	"github.com/spice-framework/spice/annotation/coretool"
	"github.com/spice-framework/spice/annotation/sdk"
)

// RequestScope constructs one bean per explicit request scope and assigns its
// cleanup to that scope.
func RequestScope() sdk.Definition {
	return sdk.Definition{
		Name:    "core.RequestScope",
		Summary: "Assigns explicit request-owned bean scope.",
		Targets: []sdk.Target{sdk.TargetType, sdk.TargetFunction, sdk.TargetMethod},
		Examples: []sdk.Example{{
			Title: "Request-owned bean",
			Code:  "// @RequestScope\ntype RequestContext struct{}",
		}},
		Compatibility: sdk.Compatibility{
			Since:        "0.3.0",
			MinimumSpice: "0.3.0",
		},
		Implementation: sdk.Implementation{
			Tool:     coretool.Path,
			Handler:  RequestScopeHandler,
			Protocol: sdk.ProtocolV1Alpha2,
		},
	}
}

// RequestScopeHandler contributes request-owned scope metadata.
func RequestScopeHandler(
	_ context.Context,
	invocation sdk.Invocation,
) (sdk.Result, error) {
	return scopeMetadata(
		invocation,
		"RequestScope",
		sdk.BeanScopeRequest,
	)
}
