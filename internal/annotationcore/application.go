package annotationcore

import (
	"context"

	"github.com/StevenBuglione/spice/annotation/sdk"
	"github.com/StevenBuglione/spice/annotation/sdk/protocol"
)

const applicationHandlerID = "core/application"

// ApplicationHandler contributes application-marker semantics for the
// canonical core.Application descriptor.
func ApplicationHandler(
	_ context.Context,
	invocation protocol.Invocation,
) (protocol.AnalyzeResult, error) {
	if err := requireDescriptor(
		invocation,
		"github.com/StevenBuglione/spice/annotation/core",
		"Application",
	); err != nil {
		return protocol.AnalyzeResult{}, err
	}
	return encodedResult(sdk.Contribution{
		Kind:        sdk.ContributionApplication,
		Application: &sdk.ApplicationContribution{},
	})
}

func encodedResult(
	value sdk.Contribution,
) (protocol.AnalyzeResult, error) {
	contribution, err := protocol.EncodeContribution(value)
	if err != nil {
		return protocol.AnalyzeResult{}, err
	}
	return protocol.AnalyzeResult{
		Contributions: []protocol.Contribution{contribution},
	}, nil
}
