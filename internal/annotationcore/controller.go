package annotationcore

import (
	"context"

	"github.com/StevenBuglione/spice/annotation/sdk"
	"github.com/StevenBuglione/spice/annotation/sdk/protocol"
)

const controllerHandlerID = "web/controller"

// ControllerHandler contributes generated net/http controller semantics.
func ControllerHandler(
	_ context.Context,
	invocation protocol.Invocation,
) (protocol.AnalyzeResult, error) {
	if err := requireDescriptor(
		invocation,
		"github.com/StevenBuglione/spice/annotation/web",
		"Controller",
	); err != nil {
		return protocol.AnalyzeResult{}, err
	}
	arguments, err := bindArguments(invocation, "", "prefix")
	if err != nil {
		return protocol.AnalyzeResult{}, err
	}
	prefix, err := stringArgument(arguments, "prefix", false)
	if err != nil {
		return protocol.AnalyzeResult{}, err
	}
	return encodedResult(sdk.Contribution{
		Kind: sdk.ContributionController,
		Controller: &sdk.ControllerContribution{
			Prefix: prefix,
		},
	})
}
