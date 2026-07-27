package annotationcore

import (
	"context"

	"github.com/StevenBuglione/spice/annotation/sdk"
	"github.com/StevenBuglione/spice/annotation/sdk/protocol"
)

const transactionalHandlerID = "data/transactional"

// TransactionalHandler contributes an explicit database transaction boundary.
func TransactionalHandler(
	_ context.Context,
	invocation protocol.Invocation,
) (protocol.AnalyzeResult, error) {
	if err := requireDescriptor(
		invocation,
		"github.com/StevenBuglione/spice/annotation/data",
		"Transactional",
	); err != nil {
		return protocol.AnalyzeResult{}, err
	}
	arguments, err := bindArguments(
		invocation,
		"",
		"isolation",
		"readOnly",
	)
	if err != nil {
		return protocol.AnalyzeResult{}, err
	}
	isolation, err := stringArgument(arguments, "isolation", false)
	if err != nil {
		return protocol.AnalyzeResult{}, err
	}
	readOnly, err := booleanArgument(arguments, "readOnly")
	if err != nil {
		return protocol.AnalyzeResult{}, err
	}
	return encodedResult(sdk.Contribution{
		Kind: sdk.ContributionTransaction,
		Transaction: &sdk.TransactionContribution{
			Isolation: isolation,
			ReadOnly:  readOnly,
		},
	})
}
