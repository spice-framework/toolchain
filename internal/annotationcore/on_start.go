package annotationcore

import (
	"context"

	"github.com/StevenBuglione/spice/annotation/sdk"
	"github.com/StevenBuglione/spice/annotation/sdk/protocol"
)

const onStartHandlerID = "lifecycle/on-start"

// OnStartHandler contributes a dependency-ordered start callback.
func OnStartHandler(
	_ context.Context,
	invocation protocol.Invocation,
) (protocol.AnalyzeResult, error) {
	return lifecycleHandler(
		invocation,
		"OnStart",
		sdk.LifecycleStart,
	)
}
