// Package security defines canonical descriptors for generated authorization
// policies.
package security

import (
	"context"

	"github.com/spice-framework/spice/annotation/coretool"
	"github.com/spice-framework/spice/annotation/sdk"
)

// Authorize attaches a secure-deny authorization policy to one HTTP route or
// interface-bound managed service method.
//
// At least one authentication, role, scope, or expression requirement is
// required.
// Multiple categories are combined with AND semantics; anyRoles requires one
// listed role while allRoles and allScopes require every listed value. The
// optional expression is compiled against Boolean `authenticated`, string
// `subject` and `issuer`, and Boolean hasRole(string)/hasScope(string)
// functions. Declaring an expression still requires an authenticated
// principal. It cannot navigate properties, look up beans, invoke arbitrary
// methods, allocate, assign, or perform I/O.
//
//	// @import { Authorize } from "github.com/spice-framework/spice/annotation/security"
//	// @Authorize(authenticated=true, anyRoles=["operator", "admin"])
func Authorize() sdk.Definition {
	return sdk.Definition{
		Name:    "security.Authorize",
		Summary: "Declares a generated secure-deny method authorization policy.",
		Targets: []sdk.Target{sdk.TargetMethod},
		Arguments: []sdk.Argument{
			{
				Name:        "authenticated",
				Kinds:       []sdk.Kind{sdk.KindBoolean},
				Description: "Require an authenticated principal.",
				Default:     "false",
			},
			{
				Name:             "anyRoles",
				Kinds:            []sdk.Kind{sdk.KindList},
				ListElementKinds: []sdk.Kind{sdk.KindString},
				Description:      "Require at least one listed role.",
			},
			{
				Name:             "allRoles",
				Kinds:            []sdk.Kind{sdk.KindList},
				ListElementKinds: []sdk.Kind{sdk.KindString},
				Description:      "Require every listed role.",
			},
			{
				Name:             "allScopes",
				Kinds:            []sdk.Kind{sdk.KindList},
				ListElementKinds: []sdk.Kind{sdk.KindString},
				Description:      "Require every listed OAuth2 scope.",
			},
			{
				Name:        "expression",
				Kinds:       []sdk.Kind{sdk.KindString},
				Description: "Restricted compiler-validated Boolean authorization expression.",
			},
		},
		Examples: []sdk.Example{{
			Title: "Authenticated operators",
			Code:  "// @Authorize(authenticated=true, anyRoles=[\"operator\"])",
		}},
		Compatibility: sdk.Compatibility{
			Since:        "0.1.0",
			MinimumSpice: "0.1.0",
		},
		Implementation: sdk.Implementation{
			Tool:     coretool.Path,
			Handler:  AuthorizeHandler,
			Protocol: sdk.ProtocolV1Alpha2,
		},
	}
}

// AuthorizeHandler contributes a generated secure-deny method policy.
func AuthorizeHandler(
	_ context.Context,
	invocation sdk.Invocation,
) (sdk.Result, error) {
	if err := invocation.RequireDescriptor(
		"github.com/spice-framework/spice/annotation/security",
		"Authorize",
	); err != nil {
		return sdk.Result{}, err
	}
	arguments, err := sdk.BindArguments(
		invocation,
		"",
		"authenticated",
		"anyRoles",
		"allRoles",
		"allScopes",
		"expression",
	)
	if err != nil {
		return sdk.Result{}, err
	}
	authenticated, err := arguments.Boolean("authenticated")
	if err != nil {
		return sdk.Result{}, err
	}
	anyRoles, err := arguments.Strings("anyRoles")
	if err != nil {
		return sdk.Result{}, err
	}
	allRoles, err := arguments.Strings("allRoles")
	if err != nil {
		return sdk.Result{}, err
	}
	allScopes, err := arguments.Strings("allScopes")
	if err != nil {
		return sdk.Result{}, err
	}
	expression, err := arguments.String("expression", false)
	if err != nil {
		return sdk.Result{}, err
	}
	return sdk.OneContribution(sdk.Contribution{
		Kind: sdk.ContributionAuthorization,
		Authorization: &sdk.AuthorizationContribution{
			Authenticated: authenticated,
			AnyRoles:      anyRoles,
			AllRoles:      allRoles,
			AllScopes:     allScopes,
			Expression:    expression,
		},
	})
}
