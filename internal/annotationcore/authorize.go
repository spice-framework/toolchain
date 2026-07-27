package annotationcore

import (
	"context"

	"github.com/StevenBuglione/spice/annotation/sdk"
	"github.com/StevenBuglione/spice/annotation/sdk/protocol"
)

const authorizeHandlerID = "security/authorize"

// AuthorizeHandler contributes a generated secure-deny route policy.
func AuthorizeHandler(
	_ context.Context,
	invocation protocol.Invocation,
) (protocol.AnalyzeResult, error) {
	if err := requireDescriptor(
		invocation,
		"github.com/StevenBuglione/spice/annotation/security",
		"Authorize",
	); err != nil {
		return protocol.AnalyzeResult{}, err
	}
	arguments, err := bindArguments(
		invocation,
		"",
		"authenticated",
		"anyRoles",
		"allRoles",
		"allScopes",
	)
	if err != nil {
		return protocol.AnalyzeResult{}, err
	}
	authenticated, err := booleanArgument(arguments, "authenticated")
	if err != nil {
		return protocol.AnalyzeResult{}, err
	}
	anyRoles, err := stringListArgument(arguments, "anyRoles")
	if err != nil {
		return protocol.AnalyzeResult{}, err
	}
	allRoles, err := stringListArgument(arguments, "allRoles")
	if err != nil {
		return protocol.AnalyzeResult{}, err
	}
	allScopes, err := stringListArgument(arguments, "allScopes")
	if err != nil {
		return protocol.AnalyzeResult{}, err
	}
	return encodedResult(sdk.Contribution{
		Kind: sdk.ContributionAuthorization,
		Authorization: &sdk.AuthorizationContribution{
			Authenticated: authenticated,
			AnyRoles:      anyRoles,
			AllRoles:      allRoles,
			AllScopes:     allScopes,
		},
	})
}
