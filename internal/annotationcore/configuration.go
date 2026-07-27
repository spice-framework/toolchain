package annotationcore

import (
	"context"

	"github.com/StevenBuglione/spice/annotation/sdk"
	"github.com/StevenBuglione/spice/annotation/sdk/protocol"
)

const configurationHandlerID = "core/configuration"

// ConfigurationHandler contributes typed configuration semantics.
func ConfigurationHandler(
	_ context.Context,
	invocation protocol.Invocation,
) (protocol.AnalyzeResult, error) {
	if err := requireDescriptor(
		invocation,
		"github.com/StevenBuglione/spice/annotation/core",
		"Configuration",
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
		Kind: sdk.ContributionConfiguration,
		Configuration: &sdk.ConfigurationContribution{
			Prefix: prefix,
		},
	})
}
