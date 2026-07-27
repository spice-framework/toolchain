package annotationcore

import (
	"context"

	"github.com/StevenBuglione/spice/annotation/sdk"
	"github.com/StevenBuglione/spice/annotation/sdk/protocol"
)

const namedInterfaceHandlerID = "modulith/named-interface"

// NamedInterfaceHandler contributes one explicitly exposed module API.
func NamedInterfaceHandler(
	_ context.Context,
	invocation protocol.Invocation,
) (protocol.AnalyzeResult, error) {
	if err := requireDescriptor(
		invocation,
		"github.com/StevenBuglione/spice/annotation/modulith",
		"NamedInterface",
	); err != nil {
		return protocol.AnalyzeResult{}, err
	}
	arguments, err := bindArguments(invocation, "name", "name")
	if err != nil {
		return protocol.AnalyzeResult{}, err
	}
	name, err := stringArgument(arguments, "name", true)
	if err != nil {
		return protocol.AnalyzeResult{}, err
	}
	return encodedResult(sdk.Contribution{
		Kind: sdk.ContributionNamedInterface,
		NamedInterface: &sdk.NamedInterfaceContribution{
			Name: name,
		},
	})
}
