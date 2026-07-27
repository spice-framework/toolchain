package annotationcore

import (
	"context"

	"github.com/StevenBuglione/spice/annotation/sdk"
	"github.com/StevenBuglione/spice/annotation/sdk/protocol"
)

const fixedDelayHandlerID = "schedule/fixed-delay"

// FixedDelayHandler contributes one generated scheduled method.
func FixedDelayHandler(
	_ context.Context,
	invocation protocol.Invocation,
) (protocol.AnalyzeResult, error) {
	if err := requireDescriptor(
		invocation,
		"github.com/StevenBuglione/spice/annotation/schedule",
		"FixedDelay",
	); err != nil {
		return protocol.AnalyzeResult{}, err
	}
	arguments, err := bindArguments(
		invocation,
		"",
		"delay",
		"initialDelay",
		"continueOnError",
	)
	if err != nil {
		return protocol.AnalyzeResult{}, err
	}
	delay, err := stringArgument(arguments, "delay", true)
	if err != nil {
		return protocol.AnalyzeResult{}, err
	}
	initialDelay, err := stringArgument(
		arguments,
		"initialDelay",
		false,
	)
	if err != nil {
		return protocol.AnalyzeResult{}, err
	}
	continueOnError, err := booleanArgument(
		arguments,
		"continueOnError",
	)
	if err != nil {
		return protocol.AnalyzeResult{}, err
	}
	return encodedResult(sdk.Contribution{
		Kind: sdk.ContributionSchedule,
		Schedule: &sdk.ScheduleContribution{
			Delay:           delay,
			InitialDelay:    initialDelay,
			ContinueOnError: continueOnError,
		},
	})
}
