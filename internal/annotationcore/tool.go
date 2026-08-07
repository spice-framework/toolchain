// Package annotationcore implements the official Go-native annotation tool.
// The compiler communicates with this package only through the public SDK
// protocol and never imports it.
package annotationcore

import (
	"context"
	"errors"
	"runtime/debug"
	"sort"
	"strings"

	asyncannotation "github.com/spice-framework/spice/annotation/async"
	cacheannotation "github.com/spice-framework/spice/annotation/cache"
	coreannotation "github.com/spice-framework/spice/annotation/core"
	dataannotation "github.com/spice-framework/spice/annotation/data"
	eventannotation "github.com/spice-framework/spice/annotation/event"
	lifecycleannotation "github.com/spice-framework/spice/annotation/lifecycle"
	managementannotation "github.com/spice-framework/spice/annotation/management"
	modulithannotation "github.com/spice-framework/spice/annotation/modulith"
	observabilityannotation "github.com/spice-framework/spice/annotation/observability"
	scheduleannotation "github.com/spice-framework/spice/annotation/schedule"
	"github.com/spice-framework/spice/annotation/sdk"
	"github.com/spice-framework/spice/annotation/sdk/protocol"
	securityannotation "github.com/spice-framework/spice/annotation/security"
	webannotation "github.com/spice-framework/spice/annotation/web"
	"github.com/spice-framework/toolchain/internal/identity"
)

const (
	toolPath             = identity.AnnotationTool
	modulePath           = identity.ToolchainModule
	descriptorModulePath = identity.CoreModule
)

var descriptorPackages = []string{
	descriptorModulePath + "/annotation/async",
	descriptorModulePath + "/annotation/cache",
	descriptorModulePath + "/annotation/core",
	descriptorModulePath + "/annotation/data",
	descriptorModulePath + "/annotation/event",
	descriptorModulePath + "/annotation/lifecycle",
	descriptorModulePath + "/annotation/management",
	descriptorModulePath + "/annotation/modulith",
	descriptorModulePath + "/annotation/observability",
	descriptorModulePath + "/annotation/schedule",
	descriptorModulePath + "/annotation/security",
	descriptorModulePath + "/annotation/web",
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
			sdk.Symbol{Package: descriptorModulePath + "/annotation/core", Name: "Application"},
			sdk.ContributionApplication,
			coreannotation.Application,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: descriptorModulePath + "/annotation/core", Name: "Service"},
			sdk.ContributionStereotype,
			coreannotation.Service,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: descriptorModulePath + "/annotation/core", Name: "Component"},
			sdk.ContributionStereotype,
			coreannotation.Component,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: descriptorModulePath + "/annotation/core", Name: "Repository"},
			sdk.ContributionStereotype,
			coreannotation.Repository,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: descriptorModulePath + "/annotation/core", Name: "Implements"},
			sdk.ContributionInterface,
			coreannotation.Implements,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: descriptorModulePath + "/annotation/core", Name: "Bean"},
			sdk.ContributionProvider,
			coreannotation.Bean,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: descriptorModulePath + "/annotation/core", Name: "Qualifier"},
			sdk.ContributionBeanMetadata,
			coreannotation.Qualifier,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: descriptorModulePath + "/annotation/core", Name: "Primary"},
			sdk.ContributionBeanMetadata,
			coreannotation.Primary,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: descriptorModulePath + "/annotation/core", Name: "Fallback"},
			sdk.ContributionBeanMetadata,
			coreannotation.Fallback,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: descriptorModulePath + "/annotation/core", Name: "Order"},
			sdk.ContributionBeanMetadata,
			coreannotation.Order,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: descriptorModulePath + "/annotation/core", Name: "Singleton"},
			sdk.ContributionBeanMetadata,
			coreannotation.Singleton,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: descriptorModulePath + "/annotation/core", Name: "Prototype"},
			sdk.ContributionBeanMetadata,
			coreannotation.Prototype,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: descriptorModulePath + "/annotation/core", Name: "RequestScope"},
			sdk.ContributionBeanMetadata,
			coreannotation.RequestScope,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: descriptorModulePath + "/annotation/core", Name: "SessionScope"},
			sdk.ContributionBeanMetadata,
			coreannotation.SessionScope,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: descriptorModulePath + "/annotation/core", Name: "Configuration"},
			sdk.ContributionStereotype,
			coreannotation.Configuration,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: descriptorModulePath + "/annotation/core", Name: "ConfigurationProperties"},
			sdk.ContributionConfiguration,
			coreannotation.ConfigurationProperties,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: descriptorModulePath + "/annotation/core", Name: "Enum"},
			sdk.ContributionEnum,
			coreannotation.Enum,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: descriptorModulePath + "/annotation/web", Name: "Controller"},
			sdk.ContributionController,
			webannotation.Controller,
			sdk.ContributionStereotype,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: descriptorModulePath + "/annotation/web", Name: "Get"},
			sdk.ContributionRoute,
			webannotation.Get,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: descriptorModulePath + "/annotation/web", Name: "Post"},
			sdk.ContributionRoute,
			webannotation.Post,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: descriptorModulePath + "/annotation/modulith", Name: "Module"},
			sdk.ContributionModule,
			modulithannotation.Module,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: descriptorModulePath + "/annotation/modulith", Name: "NamedInterface"},
			sdk.ContributionNamedInterface,
			modulithannotation.NamedInterface,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: descriptorModulePath + "/annotation/lifecycle", Name: "OnStart"},
			sdk.ContributionLifecycle,
			lifecycleannotation.OnStart,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: descriptorModulePath + "/annotation/lifecycle", Name: "OnStop"},
			sdk.ContributionLifecycle,
			lifecycleannotation.OnStop,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: descriptorModulePath + "/annotation/async", Name: "Execute"},
			sdk.ContributionAsync,
			asyncannotation.Execute,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: descriptorModulePath + "/annotation/cache", Name: "Cacheable"},
			sdk.ContributionCache,
			cacheannotation.Cacheable,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: descriptorModulePath + "/annotation/data", Name: "Transactional"},
			sdk.ContributionTransaction,
			dataannotation.Transactional,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: descriptorModulePath + "/annotation/event", Name: "Topic"},
			sdk.ContributionEventTopic,
			eventannotation.Topic,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: descriptorModulePath + "/annotation/event", Name: "Listener"},
			sdk.ContributionEventListener,
			eventannotation.Listener,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: descriptorModulePath + "/annotation/schedule", Name: "FixedDelay"},
			sdk.ContributionSchedule,
			scheduleannotation.FixedDelay,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: descriptorModulePath + "/annotation/security", Name: "Authorize"},
			sdk.ContributionAuthorization,
			securityannotation.Authorize,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: descriptorModulePath + "/annotation/management", Name: "Enable"},
			sdk.ContributionBootstrap,
			managementannotation.Enable,
		),
		newHandlerRegistration(
			sdk.Symbol{Package: descriptorModulePath + "/annotation/observability", Name: "Logging"},
			sdk.ContributionBootstrap,
			observabilityannotation.Logging,
		),
	}
}

func newHandlerRegistration(
	descriptor sdk.Symbol,
	capability sdk.ContributionKind,
	definition func() sdk.Definition,
	additional ...sdk.ContributionKind,
) handlerRegistration {
	value := definition()
	capabilities := make([]string, 1, 1+len(additional))
	capabilities[0] = string(capability)
	for _, item := range additional {
		capabilities = append(capabilities, string(item))
	}
	sort.Strings(capabilities)
	return handlerRegistration{
		description: protocol.Handler{
			Descriptor:   descriptor,
			Capabilities: capabilities,
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
