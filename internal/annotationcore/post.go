package annotationcore

import (
	"context"

	"github.com/StevenBuglione/spice/annotation/sdk"
	"github.com/StevenBuglione/spice/annotation/sdk/protocol"
)

const postHandlerID = "web/post"

// PostHandler contributes an HTTP POST route.
func PostHandler(
	_ context.Context,
	invocation protocol.Invocation,
) (protocol.AnalyzeResult, error) {
	return routeHandler(
		invocation,
		"Post",
		"POST",
	)
}

func routeHandler(
	invocation protocol.Invocation,
	symbol string,
	method string,
) (protocol.AnalyzeResult, error) {
	if err := requireDescriptor(
		invocation,
		"github.com/StevenBuglione/spice/annotation/web",
		symbol,
	); err != nil {
		return protocol.AnalyzeResult{}, err
	}
	arguments, err := bindArguments(invocation, "path", "path")
	if err != nil {
		return protocol.AnalyzeResult{}, err
	}
	routePath, err := stringArgument(arguments, "path", true)
	if err != nil {
		return protocol.AnalyzeResult{}, err
	}
	return encodedResult(sdk.Contribution{
		Kind: sdk.ContributionRoute,
		Route: &sdk.RouteContribution{
			Method: method,
			Path:   routePath,
		},
	})
}
