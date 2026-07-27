package annotationcore

import (
	"context"

	"github.com/StevenBuglione/spice/annotation/sdk"
	"github.com/StevenBuglione/spice/annotation/sdk/protocol"
)

const beanHandlerID = "core/bean"

// BeanHandler contributes exact-type provider semantics.
func BeanHandler(
	_ context.Context,
	invocation protocol.Invocation,
) (protocol.AnalyzeResult, error) {
	if err := requireDescriptor(
		invocation,
		"github.com/StevenBuglione/spice/annotation/core",
		"Bean",
	); err != nil {
		return protocol.AnalyzeResult{}, err
	}
	if _, err := bindArguments(invocation, ""); err != nil {
		return protocol.AnalyzeResult{}, err
	}
	return encodedResult(sdk.Contribution{
		Kind:     sdk.ContributionProvider,
		Provider: &sdk.ProviderContribution{},
	})
}
