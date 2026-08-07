package annotationcore

import (
	"context"
	"testing"

	"github.com/spice-framework/spice/annotation/sdk"
	"github.com/spice-framework/spice/annotation/sdk/protocol"
)

func TestToolIdentityDescriptionAndDispatch(t *testing.T) {
	t.Parallel()
	tool := New()
	identity, err := tool.Initialize(
		context.Background(),
		protocol.InitializeParams{
			Protocol: sdk.ProtocolV1Alpha2,
			ToolPath: toolPath,
		},
	)
	if err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if identity.ToolPath != toolPath ||
		identity.ModulePath != modulePath {
		t.Fatalf("Initialize() = %+v", identity)
	}
	description, err := tool.Describe(
		context.Background(),
		protocol.DescribeParams{},
	)
	if err != nil {
		t.Fatalf("Describe() error = %v", err)
	}
	if len(description.Handlers) != 33 {
		t.Fatalf("Describe() = %+v", description)
	}
	foundApplication := false
	for _, item := range description.Handlers {
		if item.Descriptor.Package ==
			"github.com/spice-framework/spice/annotation/core" &&
			item.Descriptor.Name == "Application" {
			foundApplication = true
		}
	}
	if !foundApplication {
		t.Fatalf("Describe() omitted ApplicationHandler: %+v", description)
	}
	result, err := tool.Analyze(
		context.Background(),
		protocol.AnalyzeParams{
			Descriptor: sdk.Symbol{
				Package: "github.com/spice-framework/spice/annotation/core",
				Name:    "Application",
			},
			Invocation: protocol.Invocation{
				DescriptorPackage: "github.com/spice-framework/spice/annotation/core",
				DescriptorSymbol:  "Application",
			},
		},
	)
	if err != nil || len(result.Contributions) != 1 {
		t.Fatalf("Analyze() = %+v, %v", result, err)
	}
	if err := tool.Shutdown(
		context.Background(),
		protocol.ShutdownParams{},
	); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

func TestToolFailsClosedOnInvalidStateAndNegotiation(t *testing.T) {
	t.Parallel()
	var nilTool *Tool
	if _, err := nilTool.Initialize(
		context.Background(),
		protocol.InitializeParams{},
	); err == nil {
		t.Fatal("nil Initialize() error = nil")
	}
	if _, err := nilTool.Describe(
		context.Background(),
		protocol.DescribeParams{},
	); err == nil {
		t.Fatal("nil Describe() error = nil")
	}
	if _, err := nilTool.Analyze(
		context.Background(),
		protocol.AnalyzeParams{},
	); err == nil {
		t.Fatal("nil Analyze() error = nil")
	}
	if err := nilTool.Shutdown(
		context.Background(),
		protocol.ShutdownParams{},
	); err == nil {
		t.Fatal("nil Shutdown() error = nil")
	}

	tool := New()
	for _, params := range []protocol.InitializeParams{
		{
			Protocol: sdk.ProtocolVersion("spice.annotation/v1alpha1"),
			ToolPath: toolPath,
		},
		{
			Protocol: sdk.ProtocolVersion("unsupported"),
			ToolPath: toolPath,
		},
		{
			Protocol: sdk.ProtocolV1Alpha2,
			ToolPath: "example.com/wrong",
		},
	} {
		if _, err := tool.Initialize(
			context.Background(),
			params,
		); err == nil {
			t.Fatalf("Initialize(%+v) error = nil", params)
		}
	}
	if _, err := tool.Analyze(
		context.Background(),
		protocol.AnalyzeParams{
			Descriptor: sdk.Symbol{
				Package: "example.com/missing",
				Name:    "Missing",
			},
			Invocation: sdk.Invocation{
				DescriptorPackage: "example.com/missing",
				DescriptorSymbol:  "Missing",
			},
		},
	); err == nil {
		t.Fatal("Analyze(missing) error = nil")
	}
}
