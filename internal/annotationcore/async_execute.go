package annotationcore

import (
	"context"

	"github.com/StevenBuglione/spice/annotation/sdk"
	"github.com/StevenBuglione/spice/annotation/sdk/protocol"
)

const asyncExecuteHandlerID = "async/execute"

// AsyncExecuteHandler contributes generated asynchronous execution semantics.
func AsyncExecuteHandler(
	_ context.Context,
	invocation protocol.Invocation,
) (protocol.AnalyzeResult, error) {
	if err := requireDescriptor(
		invocation,
		"github.com/StevenBuglione/spice/annotation/async",
		"Execute",
	); err != nil {
		return protocol.AnalyzeResult{}, err
	}
	if _, err := bindArguments(invocation, ""); err != nil {
		return protocol.AnalyzeResult{}, err
	}
	return encodedResult(sdk.Contribution{
		Kind:  sdk.ContributionAsync,
		Async: &sdk.AsyncContribution{},
	})
}
