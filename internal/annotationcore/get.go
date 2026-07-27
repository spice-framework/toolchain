package annotationcore

import (
	"context"

	"github.com/StevenBuglione/spice/annotation/sdk/protocol"
)

const getHandlerID = "web/get"

// GetHandler contributes an HTTP GET route.
func GetHandler(
	_ context.Context,
	invocation protocol.Invocation,
) (protocol.AnalyzeResult, error) {
	return routeHandler(
		invocation,
		"Get",
		"GET",
	)
}
