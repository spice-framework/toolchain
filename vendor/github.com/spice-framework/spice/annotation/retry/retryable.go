// Package retry defines canonical descriptors for generated bounded retry
// policies.
package retry

import (
	"context"

	"github.com/spice-framework/spice/annotation/coretool"
	"github.com/spice-framework/spice/annotation/sdk"
)

// Retryable marks an interface-bound managed service method for bounded,
// context-aware retry. The optional classifier names an exported same-package
// func(error) bool. Without one, generated code uses retry.Transient, which
// retries only explicitly transient errors and never cancellation.
//
//	// @import { Retryable } from "github.com/spice-framework/spice/annotation/retry"
//	// @Retryable(maxAttempts=3, classifier=IsTransient)
func Retryable() sdk.Definition {
	return sdk.Definition{
		Name:    "retry.Retryable",
		Summary: "Declares a generated bounded service-method retry policy.",
		Targets: []sdk.Target{sdk.TargetMethod},
		Arguments: []sdk.Argument{
			{
				Name:        "maxAttempts",
				Kinds:       []sdk.Kind{sdk.KindInteger},
				Description: "Maximum attempts, including the initial call.",
				Default:     "3",
			},
			{
				Name:        "initialBackoff",
				Kinds:       []sdk.Kind{sdk.KindString},
				Description: "Initial context-aware backoff duration.",
				Default:     "100ms",
			},
			{
				Name:        "maxBackoff",
				Kinds:       []sdk.Kind{sdk.KindString},
				Description: "Maximum context-aware backoff duration.",
				Default:     "1s",
			},
			{
				Name:        "multiplier",
				Kinds:       []sdk.Kind{sdk.KindInteger},
				Description: "Exponential backoff multiplier.",
				Default:     "2",
			},
			{
				Name:        "classifier",
				Kinds:       []sdk.Kind{sdk.KindIdentifier},
				Description: "Optional exported same-package func(error) bool.",
			},
		},
		Examples: []sdk.Example{{
			Title: "Retry transient failures",
			Code:  "// @Retryable(maxAttempts=3, classifier=IsTransient)",
		}},
		Compatibility: sdk.Compatibility{
			Since:        "0.2.0",
			MinimumSpice: "0.2.0",
		},
		Implementation: sdk.Implementation{
			Tool:     coretool.Path,
			Handler:  RetryableHandler,
			Protocol: sdk.ProtocolV1Alpha2,
		},
	}
}

// RetryableHandler contributes one bounded retry policy.
func RetryableHandler(
	_ context.Context,
	invocation sdk.Invocation,
) (sdk.Result, error) {
	if err := invocation.RequireDescriptor(
		"github.com/spice-framework/spice/annotation/retry",
		"Retryable",
	); err != nil {
		return sdk.Result{}, err
	}
	arguments, err := sdk.BindArguments(
		invocation,
		"",
		"maxAttempts",
		"initialBackoff",
		"maxBackoff",
		"multiplier",
		"classifier",
	)
	if err != nil {
		return sdk.Result{}, err
	}
	maxAttempts := int64(3)
	if _, present := arguments["maxAttempts"]; present {
		maxAttempts, err = arguments.Integer("maxAttempts")
		if err != nil {
			return sdk.Result{}, err
		}
	}
	initialBackoff := "100ms"
	if _, present := arguments["initialBackoff"]; present {
		initialBackoff, err = arguments.String("initialBackoff", true)
		if err != nil {
			return sdk.Result{}, err
		}
	}
	maxBackoff := "1s"
	if _, present := arguments["maxBackoff"]; present {
		maxBackoff, err = arguments.String("maxBackoff", true)
		if err != nil {
			return sdk.Result{}, err
		}
	}
	multiplier := int64(2)
	if _, present := arguments["multiplier"]; present {
		multiplier, err = arguments.Integer("multiplier")
		if err != nil {
			return sdk.Result{}, err
		}
	}
	classifier, err := arguments.Identifier("classifier", false)
	if err != nil {
		return sdk.Result{}, err
	}
	return sdk.OneContribution(sdk.Contribution{
		Kind: sdk.ContributionRetry,
		Retry: &sdk.RetryContribution{
			MaxAttempts:    maxAttempts,
			InitialBackoff: initialBackoff,
			MaxBackoff:     maxBackoff,
			Multiplier:     multiplier,
			Classifier:     classifier,
		},
	})
}
