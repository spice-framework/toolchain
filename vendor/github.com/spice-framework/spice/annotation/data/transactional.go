// Package data defines canonical descriptors for generated data boundaries.
package data

import (
	"context"

	"github.com/spice-framework/spice/annotation/coretool"
	"github.com/spice-framework/spice/annotation/sdk"
)

// Transactional marks a provider-owned method that must run inside an explicit
// database/sql transaction.
//
// The generated wrapper propagates context, commits only on success, rolls
// back on error or panic, and records the owning application module.
// Isolation is optional and readOnly defaults to false.
//
//	// @import { Transactional } from "github.com/spice-framework/spice/annotation/data"
//	// @Transactional(isolation="serializable")
func Transactional() sdk.Definition {
	return sdk.Definition{
		Name:    "data.Transactional",
		Summary: "Declares a generated database transaction boundary.",
		Targets: []sdk.Target{sdk.TargetMethod},
		Arguments: []sdk.Argument{
			{
				Name:          "isolation",
				Kinds:         []sdk.Kind{sdk.KindString},
				AllowedValues: []string{"", "default", "read-uncommitted", "read-committed", "write-committed", "repeatable-read", "snapshot", "serializable", "linearizable"},
				Description:   "database/sql isolation level name.",
			},
			{
				Name:        "readOnly",
				Kinds:       []sdk.Kind{sdk.KindBoolean},
				Description: "Whether the transaction is declared read-only.",
				Default:     "false",
			},
		},
		Examples: []sdk.Example{{
			Title: "Serializable transaction",
			Code:  "// @Transactional(isolation=\"serializable\")",
		}},
		Compatibility: sdk.Compatibility{
			Since:        "0.1.0",
			MinimumSpice: "0.1.0",
		},
		Implementation: sdk.Implementation{
			Tool:     coretool.Path,
			Handler:  TransactionalHandler,
			Protocol: sdk.ProtocolV1Alpha2,
		},
	}
}

// TransactionalHandler contributes an explicit database transaction boundary.
func TransactionalHandler(
	_ context.Context,
	invocation sdk.Invocation,
) (sdk.Result, error) {
	if err := invocation.RequireDescriptor(
		"github.com/spice-framework/spice/annotation/data",
		"Transactional",
	); err != nil {
		return sdk.Result{}, err
	}
	arguments, err := sdk.BindArguments(
		invocation,
		"",
		"isolation",
		"readOnly",
	)
	if err != nil {
		return sdk.Result{}, err
	}
	isolation, err := arguments.String("isolation", false)
	if err != nil {
		return sdk.Result{}, err
	}
	readOnly, err := arguments.Boolean("readOnly")
	if err != nil {
		return sdk.Result{}, err
	}
	return sdk.OneContribution(sdk.Contribution{
		Kind: sdk.ContributionTransaction,
		Transaction: &sdk.TransactionContribution{
			Isolation: isolation,
			ReadOnly:  readOnly,
		},
	})
}
