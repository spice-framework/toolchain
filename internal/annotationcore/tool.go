// Package annotationcore implements the official Go-native annotation tool.
// The compiler communicates with this package only through the public SDK
// protocol and never imports it.
package annotationcore

import (
	"context"
	"errors"
	"runtime/debug"
	"strings"

	asyncannotation "github.com/StevenBuglione/spice/annotation/async"
	cacheannotation "github.com/StevenBuglione/spice/annotation/cache"
	coreannotation "github.com/StevenBuglione/spice/annotation/core"
	"github.com/StevenBuglione/spice/annotation/coretool"
	dataannotation "github.com/StevenBuglione/spice/annotation/data"
	eventannotation "github.com/StevenBuglione/spice/annotation/event"
	lifecycleannotation "github.com/StevenBuglione/spice/annotation/lifecycle"
	managementannotation "github.com/StevenBuglione/spice/annotation/management"
	modulithannotation "github.com/StevenBuglione/spice/annotation/modulith"
	observabilityannotation "github.com/StevenBuglione/spice/annotation/observability"
	scheduleannotation "github.com/StevenBuglione/spice/annotation/schedule"
	"github.com/StevenBuglione/spice/annotation/sdk"
	"github.com/StevenBuglione/spice/annotation/sdk/protocol"
	securityannotation "github.com/StevenBuglione/spice/annotation/security"
	webannotation "github.com/StevenBuglione/spice/annotation/web"
)

const (
	toolPath   = coretool.Path
	modulePath = "github.com/StevenBuglione/spice"
)

var descriptorPackages = []string{
	modulePath + "/annotation/async",
	modulePath + "/annotation/cache",
	modulePath + "/annotation/core",
	modulePath + "/annotation/data",
	modulePath + "/annotation/event",
	modulePath + "/annotation/lifecycle",
	modulePath + "/annotation/management",
	modulePath + "/annotation/modulith",
	modulePath + "/annotation/observability",
	modulePath + "/annotation/schedule",
	modulePath + "/annotation/security",
	modulePath + "/annotation/web",
}

// Tool is the official deterministic annotation protocol implementation.
type Tool struct {
	moduleVersion string
	handlers      map[string]sdk.Handler
	descriptions  []protocol.Handler
}

// New returns an isolated official annotation tool.
func New() *Tool {
	registrations := handlerRegistrations()
	tool := &Tool{
		moduleVersion: selectedModuleVersion(),
		handlers:      make(map[string]sdk.Handler, len(registrations)),
		descriptions:  make([]protocol.Handler, len(registrations)),
	}
	for index, registration := range registrations {
		tool.handlers[symbolKey(registration.description.Descriptor)] = registration.handle
		tool.descriptions[index] = registration.description
	}
	return tool
}

// Initialize validates protocol and executable identity.
func (tool *Tool) Initialize(
	_ context.Context,
	params protocol.InitializeParams,
) (protocol.InitializeResult, error) {
	if tool == nil {
		return protocol.InitializeResult{}, errors.New(
			"annotation core tool is nil",
		)
	}
	if params.Protocol != sdk.ProtocolV1Alpha2 {
		return protocol.InitializeResult{}, errors.New(
			"annotation core tool requires protocol " +
				string(sdk.ProtocolV1Alpha2),
		)
	}
	if params.ToolPath != toolPath {
		return protocol.InitializeResult{}, errors.New(
			"annotation core tool path identity does not match",
		)
	}
	return protocol.InitializeResult{
		Protocol:      sdk.ProtocolV1Alpha2,
		ToolPath:      toolPath,
		ModulePath:    modulePath,
		ModuleVersion: tool.moduleVersion,
	}, nil
}

// Describe returns every stable handler and implementation source symbol.
func (tool *Tool) Describe(
	context.Context,
	protocol.DescribeParams,
) (protocol.DescribeResult, error) {
	if tool == nil {
		return protocol.DescribeResult{}, errors.New(
			"annotation core tool is nil",
		)
	}
	result := make([]protocol.Handler, len(tool.descriptions))
	copy(result, tool.descriptions)
	return protocol.DescribeResult{
		DescriptorPackages: append(
			[]string(nil),
			descriptorPackages...,
		),
		Handlers: result,
	}, nil
}

// Analyze dispatches only a handler identity declared by Describe.
func (tool *Tool) Analyze(
	ctx context.Context,
	params protocol.AnalyzeParams,
) (protocol.AnalyzeResult, error) {
	if tool == nil {
		return protocol.AnalyzeResult{}, errors.New(
			"annotation core tool is nil",
		)
	}
	if params.Descriptor.Package != params.Invocation.DescriptorPackage ||
		params.Descriptor.Name != params.Invocation.DescriptorSymbol {
		return protocol.AnalyzeResult{}, errors.New(
			"annotation descriptor dispatch does not match invocation",
		)
	}
	selected, found := tool.handlers[symbolKey(params.Descriptor)]
	if !found {
		return protocol.AnalyzeResult{}, errors.New(
			"annotation core handler is not declared",
		)
	}
	result, err := selected(ctx, params.Invocation)
	if err != nil {
		return protocol.AnalyzeResult{}, err
	}
	return encodeHandlerResult(result)
}

// Shutdown releases no global resources because Tool owns none.
func (tool *Tool) Shutdown(
	context.Context,
	protocol.ShutdownParams,
) error {
	if tool == nil {
		return errors.New("annotation core tool is nil")
	}
	return nil
}

func selectedModuleVersion() string {
	build, found := debug.ReadBuildInfo()
	if !found || build.Main.Path != modulePath {
		return ""
	}
	version := strings.TrimSpace(build.Main.Version)
	if version == "(devel)" {
		return ""
	}
	return version
}

type handlerRegistration struct {
	description protocol.Handler
	handle      sdk.Handler
}

func handlerRegistrations() []handlerRegistration {
	return []handlerRegistration{
		newHandlerRegistration(
			sdk.Symbol{Package: modulePath + "/annotation/core", Name: "Application"},
			sdk.ContributionApplication,
			coreannotation.Application,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: modulePath + "/annotation/core", Name: "Service"},
			sdk.ContributionStereotype,
			coreannotation.Service,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: modulePath + "/annotation/core", Name: "Bean"},
			sdk.ContributionProvider,
			coreannotation.Bean,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: modulePath + "/annotation/core", Name: "Configuration"},
			sdk.ContributionConfiguration,
			coreannotation.Configuration,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: modulePath + "/annotation/web", Name: "Controller"},
			sdk.ContributionController,
			webannotation.Controller,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: modulePath + "/annotation/web", Name: "Get"},
			sdk.ContributionRoute,
			webannotation.Get,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: modulePath + "/annotation/web", Name: "Post"},
			sdk.ContributionRoute,
			webannotation.Post,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: modulePath + "/annotation/modulith", Name: "Module"},
			sdk.ContributionModule,
			modulithannotation.Module,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: modulePath + "/annotation/modulith", Name: "NamedInterface"},
			sdk.ContributionNamedInterface,
			modulithannotation.NamedInterface,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: modulePath + "/annotation/lifecycle", Name: "OnStart"},
			sdk.ContributionLifecycle,
			lifecycleannotation.OnStart,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: modulePath + "/annotation/lifecycle", Name: "OnStop"},
			sdk.ContributionLifecycle,
			lifecycleannotation.OnStop,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: modulePath + "/annotation/async", Name: "Execute"},
			sdk.ContributionAsync,
			asyncannotation.Execute,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: modulePath + "/annotation/cache", Name: "Cacheable"},
			sdk.ContributionCache,
			cacheannotation.Cacheable,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: modulePath + "/annotation/data", Name: "Transactional"},
			sdk.ContributionTransaction,
			dataannotation.Transactional,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: modulePath + "/annotation/event", Name: "Topic"},
			sdk.ContributionEventTopic,
			eventannotation.Topic,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: modulePath + "/annotation/event", Name: "Listener"},
			sdk.ContributionEventListener,
			eventannotation.Listener,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: modulePath + "/annotation/schedule", Name: "FixedDelay"},
			sdk.ContributionSchedule,
			scheduleannotation.FixedDelay,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: modulePath + "/annotation/security", Name: "Authorize"},
			sdk.ContributionAuthorization,
			securityannotation.Authorize,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: modulePath + "/annotation/management", Name: "Enable"},
			sdk.ContributionBootstrap,
			managementannotation.Enable,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: modulePath + "/annotation/observability", Name: "Logging"},
			sdk.ContributionBootstrap,
			observabilityannotation.Logging,
		),
	}
}

func newHandlerRegistration(
	descriptor sdk.Symbol,
	capability sdk.ContributionKind,
	definition func() sdk.Definition,
) handlerRegistration {
	value := definition()
	return handlerRegistration{
		description: protocol.Handler{
			Descriptor:   descriptor,
			Capabilities: []string{string(capability)},
		},
		handle: value.Implementation.Handler,
	}
}

func symbolKey(symbol sdk.Symbol) string {
	return symbol.Package + "\x00" + symbol.Name
}

func encodeHandlerResult(value sdk.Result) (protocol.AnalyzeResult, error) {
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
		encoded, err := protocol.EncodeContribution(contribution)
		if err != nil {
			return protocol.AnalyzeResult{}, err
		}
		result.Contributions[index] = encoded
	}
	return result, nil
}
