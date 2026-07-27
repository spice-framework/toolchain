package annotationcore

import (
	"context"
	"errors"

	"github.com/StevenBuglione/spice/annotation/sdk"
	"github.com/StevenBuglione/spice/annotation/sdk/protocol"
)

const managementEnableHandlerID = "management/enable"

// ManagementEnableHandler contributes selected generated management
// endpoints.
func ManagementEnableHandler(
	_ context.Context,
	invocation protocol.Invocation,
) (protocol.AnalyzeResult, error) {
	if err := requireDescriptor(
		invocation,
		"github.com/StevenBuglione/spice/annotation/management",
		"Enable",
	); err != nil {
		return protocol.AnalyzeResult{}, err
	}
	arguments, err := bindArguments(invocation, "", "expose")
	if err != nil {
		return protocol.AnalyzeResult{}, err
	}
	if _, found := arguments["expose"]; !found {
		return protocol.AnalyzeResult{}, errors.New(
			"annotation argument \"expose\" is required",
		)
	}
	expose, err := stringListArgument(arguments, "expose")
	if err != nil {
		return protocol.AnalyzeResult{}, err
	}
	values := make([]sdk.ContributionValue, len(expose))
	for index, endpoint := range expose {
		values[index] = sdk.ContributionValue{
			Kind:   sdk.KindString,
			String: endpoint,
		}
	}
	return encodedResult(sdk.Contribution{
		Kind: sdk.ContributionBootstrap,
		Bootstrap: &sdk.BootstrapContribution{
			Capability: "management",
			Options: []sdk.BootstrapOption{{
				Name: "expose",
				Value: sdk.ContributionValue{
					Kind: sdk.KindList,
					List: values,
				},
			}},
		},
	})
}
