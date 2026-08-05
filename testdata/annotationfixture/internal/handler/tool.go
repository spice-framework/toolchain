// Package handler implements the fixture's public annotation protocol tool
// without importing any Spice compiler or CLI package.
package handler

import (
	"context"
	"errors"

	policyannotation "example.com/spice-annotation-fixture/annotation/policy"
	wiringannotation "example.com/spice-annotation-fixture/annotation/wiring"
	"github.com/spice-framework/spice/annotation/sdk"
	"github.com/spice-framework/spice/annotation/sdk/protocol"
)

const (
	toolPath   = "example.com/spice-annotation-fixture/cmd/spice-annotations"
	modulePath = "example.com/spice-annotation-fixture"
)

// Tool is one isolated fixture annotation protocol implementation.
type Tool struct {
	handlers map[string]sdk.Handler
}

// New registers the same typed handlers exposed by the descriptor literals.
func New() Tool {
	return Tool{
		handlers: map[string]sdk.Handler{
			symbolKey(sdk.Symbol{
				Package: modulePath + "/annotation/policy",
				Name:    "Policy",
			}): policyannotation.Policy().Implementation.Handler,
			symbolKey(sdk.Symbol{
				Package: modulePath + "/annotation/wiring",
				Name:    "Factory",
			}): wiringannotation.Factory().Implementation.Handler,
		},
	}
}

// Initialize validates protocol and Go tool identities.
func (Tool) Initialize(
	_ context.Context,
	params protocol.InitializeParams,
) (protocol.InitializeResult, error) {
	if params.Protocol != sdk.ProtocolV1Alpha2 ||
		params.ToolPath != toolPath {
		return protocol.InitializeResult{}, errors.New(
			"fixture annotation tool identity does not match",
		)
	}
	return protocol.InitializeResult{
		Protocol:      sdk.ProtocolV1Alpha2,
		ToolPath:      toolPath,
		ModulePath:    modulePath,
		ModuleVersion: "v0.0.0",
	}, nil
}

// Describe returns every fixture descriptor registered with a typed handler.
func (Tool) Describe(
	context.Context,
	protocol.DescribeParams,
) (protocol.DescribeResult, error) {
	return protocol.DescribeResult{
		DescriptorPackages: []string{
			modulePath + "/annotation/policy",
			modulePath + "/annotation/wiring",
		},
		Handlers: []protocol.Handler{
			{
				Descriptor: sdk.Symbol{
					Package: modulePath + "/annotation/policy",
					Name:    "Policy",
				},
				Capabilities: []string{string(sdk.ContributionStereotype)},
			},
			{
				Descriptor: sdk.Symbol{
					Package: modulePath + "/annotation/wiring",
					Name:    "Factory",
				},
				Capabilities: []string{string(sdk.ContributionProvider)},
			},
		},
	}, nil
}

// Analyze dispatches only descriptors registered by New.
func (tool Tool) Analyze(
	ctx context.Context,
	params protocol.AnalyzeParams,
) (protocol.AnalyzeResult, error) {
	handler, found := tool.handlers[symbolKey(params.Descriptor)]
	if !found {
		return protocol.AnalyzeResult{}, errors.New(
			"fixture annotation descriptor is not registered",
		)
	}
	value, err := handler(ctx, params.Invocation)
	if err != nil {
		return protocol.AnalyzeResult{}, err
	}
	result := protocol.AnalyzeResult{
		Contributions: make(
			[]protocol.Contribution,
			len(value.Contributions),
		),
		Diagnostics: append(
			[]protocol.Diagnostic(nil),
			value.Diagnostics...,
		),
	}
	for index, contribution := range value.Contributions {
		encoded, encodeErr := protocol.EncodeContribution(contribution)
		if encodeErr != nil {
			return protocol.AnalyzeResult{}, encodeErr
		}
		result.Contributions[index] = encoded
	}
	return result, nil
}

// Shutdown releases no resources because Tool owns no globals.
func (Tool) Shutdown(
	context.Context,
	protocol.ShutdownParams,
) error {
	return nil
}

func symbolKey(symbol sdk.Symbol) string {
	return symbol.Package + "\x00" + symbol.Name
}
