package annotationcore

import (
	"context"

	"github.com/StevenBuglione/spice/annotation/sdk"
	"github.com/StevenBuglione/spice/annotation/sdk/protocol"
)

const cacheableHandlerID = "cache/cacheable"

// CacheableHandler contributes one named generated cache boundary.
func CacheableHandler(
	_ context.Context,
	invocation protocol.Invocation,
) (protocol.AnalyzeResult, error) {
	if err := requireDescriptor(
		invocation,
		"github.com/StevenBuglione/spice/annotation/cache",
		"Cacheable",
	); err != nil {
		return protocol.AnalyzeResult{}, err
	}
	arguments, err := bindArguments(invocation, "", "name")
	if err != nil {
		return protocol.AnalyzeResult{}, err
	}
	name, err := stringArgument(arguments, "name", true)
	if err != nil {
		return protocol.AnalyzeResult{}, err
	}
	return encodedResult(sdk.Contribution{
		Kind: sdk.ContributionCache,
		Cache: &sdk.CacheContribution{
			Name: name,
		},
	})
}
