package annotationcore

import (
	"context"

	"github.com/StevenBuglione/spice/annotation/sdk"
	"github.com/StevenBuglione/spice/annotation/sdk/protocol"
)

const eventListenerHandlerID = "event/listener"

// EventListenerHandler contributes one ordered typed event listener.
func EventListenerHandler(
	_ context.Context,
	invocation protocol.Invocation,
) (protocol.AnalyzeResult, error) {
	if err := requireDescriptor(
		invocation,
		"github.com/StevenBuglione/spice/annotation/event",
		"Listener",
	); err != nil {
		return protocol.AnalyzeResult{}, err
	}
	arguments, err := bindArguments(invocation, "", "order")
	if err != nil {
		return protocol.AnalyzeResult{}, err
	}
	order, err := integerArgument(arguments, "order")
	if err != nil {
		return protocol.AnalyzeResult{}, err
	}
	return encodedResult(sdk.Contribution{
		Kind: sdk.ContributionEventListener,
		EventListener: &sdk.EventListenerContribution{
			Order: order,
		},
	})
}
