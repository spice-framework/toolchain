package annotationcore

import (
	"context"
	"testing"

	"github.com/StevenBuglione/spice/annotation/sdk"
	"github.com/StevenBuglione/spice/annotation/sdk/protocol"
)

func TestApplicationHandlerReturnsTypedContribution(t *testing.T) {
	t.Parallel()
	result, err := ApplicationHandler(
		context.Background(),
		protocol.Invocation{
			DescriptorPackage: "github.com/StevenBuglione/spice/annotation/core",
			DescriptorSymbol:  "Application",
		},
	)
	if err != nil {
		t.Fatalf("ApplicationHandler() error = %v", err)
	}
	if len(result.Contributions) != 1 {
		t.Fatalf(
			"ApplicationHandler() contributions = %d, want 1",
			len(result.Contributions),
		)
	}
	decoded, err := protocol.DecodeContribution(result.Contributions[0])
	if err != nil {
		t.Fatalf("DecodeContribution() error = %v", err)
	}
	if decoded.Kind != sdk.ContributionApplication ||
		decoded.Application == nil {
		t.Fatalf("decoded contribution = %+v", decoded)
	}
}

func TestApplicationHandlerRejectsAnotherDescriptor(t *testing.T) {
	t.Parallel()
	_, err := ApplicationHandler(
		context.Background(),
		protocol.Invocation{
			DescriptorPackage: "example.com/mimic/core",
			DescriptorSymbol:  "Application",
		},
	)
	if err == nil {
		t.Fatal("ApplicationHandler() error = nil, want failure")
	}
}
