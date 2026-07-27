package annotationcore

import (
	"context"

	"github.com/StevenBuglione/spice/annotation/sdk"
	"github.com/StevenBuglione/spice/annotation/sdk/protocol"
)

const serviceHandlerID = "core/service"

// ServiceHandler contributes an inspectable service stereotype without
// introducing implicit construction or container behavior.
func ServiceHandler(
	_ context.Context,
	invocation protocol.Invocation,
) (protocol.AnalyzeResult, error) {
	if err := requireDescriptor(
		invocation,
		"github.com/StevenBuglione/spice/annotation/core",
		"Service",
	); err != nil {
		return protocol.AnalyzeResult{}, err
	}
	if _, err := bindArguments(invocation, ""); err != nil {
		return protocol.AnalyzeResult{}, err
	}
	return encodedResult(sdk.Contribution{
		Kind: sdk.ContributionStereotype,
		Stereotype: &sdk.StereotypeContribution{
			Role: "service",
		},
	})
}
