package annotationcore

import (
	"context"

	"github.com/StevenBuglione/spice/annotation/sdk"
	"github.com/StevenBuglione/spice/annotation/sdk/protocol"
)

const moduleHandlerID = "modulith/module"

// ModuleHandler contributes an application-module root and its allowed
// dependencies.
func ModuleHandler(
	_ context.Context,
	invocation protocol.Invocation,
) (protocol.AnalyzeResult, error) {
	if err := requireDescriptor(
		invocation,
		"github.com/StevenBuglione/spice/annotation/modulith",
		"Module",
	); err != nil {
		return protocol.AnalyzeResult{}, err
	}
	arguments, err := bindArguments(
		invocation,
		"",
		"allowedDependencies",
	)
	if err != nil {
		return protocol.AnalyzeResult{}, err
	}
	allowed, err := stringListArgument(arguments, "allowedDependencies")
	if err != nil {
		return protocol.AnalyzeResult{}, err
	}
	return encodedResult(sdk.Contribution{
		Kind: sdk.ContributionModule,
		Module: &sdk.ModuleContribution{
			AllowedDependencies: allowed,
		},
	})
}
