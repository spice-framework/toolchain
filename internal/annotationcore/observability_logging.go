package annotationcore

import (
	"context"

	"github.com/StevenBuglione/spice/annotation/sdk"
	"github.com/StevenBuglione/spice/annotation/sdk/protocol"
)

const observabilityLoggingHandlerID = "observability/logging"

// ObservabilityLoggingHandler contributes structured lifecycle logging.
func ObservabilityLoggingHandler(
	_ context.Context,
	invocation protocol.Invocation,
) (protocol.AnalyzeResult, error) {
	if err := requireDescriptor(
		invocation,
		"github.com/StevenBuglione/spice/annotation/observability",
		"Logging",
	); err != nil {
		return protocol.AnalyzeResult{}, err
	}
	if _, err := bindArguments(invocation, ""); err != nil {
		return protocol.AnalyzeResult{}, err
	}
	return encodedResult(sdk.Contribution{
		Kind: sdk.ContributionBootstrap,
		Bootstrap: &sdk.BootstrapContribution{
			Capability: "observability.logging",
		},
	})
}
