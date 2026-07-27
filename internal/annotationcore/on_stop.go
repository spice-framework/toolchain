package annotationcore

import (
	"context"

	"github.com/StevenBuglione/spice/annotation/sdk"
	"github.com/StevenBuglione/spice/annotation/sdk/protocol"
)

const onStopHandlerID = "lifecycle/on-stop"

// OnStopHandler contributes a reverse-dependency stop callback.
func OnStopHandler(
	_ context.Context,
	invocation protocol.Invocation,
) (protocol.AnalyzeResult, error) {
	return lifecycleHandler(
		invocation,
		"OnStop",
		sdk.LifecycleStop,
	)
}

func lifecycleHandler(
	invocation protocol.Invocation,
	symbol string,
	phase sdk.LifecyclePhase,
) (protocol.AnalyzeResult, error) {
	if err := requireDescriptor(
		invocation,
		"github.com/StevenBuglione/spice/annotation/lifecycle",
		symbol,
	); err != nil {
		return protocol.AnalyzeResult{}, err
	}
	if _, err := bindArguments(invocation, ""); err != nil {
		return protocol.AnalyzeResult{}, err
	}
	return encodedResult(sdk.Contribution{
		Kind: sdk.ContributionLifecycle,
		Lifecycle: &sdk.LifecycleContribution{
			Phase: phase,
		},
	})
}
