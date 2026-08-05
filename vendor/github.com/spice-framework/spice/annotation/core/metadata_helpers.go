package core

import (
	"github.com/spice-framework/spice/annotation/sdk"
)

func markerMetadata(
	invocation sdk.Invocation,
	name string,
	metadata sdk.BeanMetadataContribution,
) (sdk.Result, error) {
	if err := invocation.RequireDescriptor(
		"github.com/spice-framework/spice/annotation/core",
		name,
	); err != nil {
		return sdk.Result{}, err
	}
	if _, err := sdk.BindArguments(invocation, ""); err != nil {
		return sdk.Result{}, err
	}
	return sdk.OneContribution(sdk.Contribution{
		Kind:         sdk.ContributionBeanMetadata,
		BeanMetadata: &metadata,
	})
}

func scopeMetadata(
	invocation sdk.Invocation,
	name string,
	scope sdk.BeanScope,
) (sdk.Result, error) {
	return markerMetadata(
		invocation,
		name,
		sdk.BeanMetadataContribution{Scope: scope},
	)
}
