// Package annotationcore implements the official Go-native annotation tool.
// The compiler communicates with this package only through the public SDK
// protocol and never imports it.
package annotationcore

import (
	"context"
	"errors"
	"runtime/debug"
	"strings"

	"github.com/StevenBuglione/spice/annotation/sdk"
	"github.com/StevenBuglione/spice/annotation/sdk/protocol"
)

const (
	toolPath   = "github.com/StevenBuglione/spice/cmd/spice-annotation-core"
	modulePath = "github.com/StevenBuglione/spice"
)

type handler func(
	context.Context,
	protocol.Invocation,
) (protocol.AnalyzeResult, error)

// Tool is the official deterministic annotation protocol implementation.
type Tool struct {
	moduleVersion string
	handlers      map[string]handler
	descriptions  []protocol.Handler
}

// New returns an isolated official annotation tool.
func New() *Tool {
	registrations := handlerRegistrations()
	tool := &Tool{
		moduleVersion: selectedModuleVersion(),
		handlers:      make(map[string]handler, len(registrations)),
		descriptions:  make([]protocol.Handler, len(registrations)),
	}
	for index, registration := range registrations {
		tool.handlers[registration.description.ID] = registration.handle
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
	if params.Protocol != sdk.ProtocolV1Alpha1 {
		return protocol.InitializeResult{}, errors.New(
			"annotation core tool requires protocol " +
				string(sdk.ProtocolV1Alpha1),
		)
	}
	if params.ToolPath != toolPath {
		return protocol.InitializeResult{}, errors.New(
			"annotation core tool path identity does not match",
		)
	}
	return protocol.InitializeResult{
		Protocol:      sdk.ProtocolV1Alpha1,
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
	return protocol.DescribeResult{Handlers: result}, nil
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
	selected, found := tool.handlers[params.Handler]
	if !found {
		return protocol.AnalyzeResult{}, errors.New(
			"annotation core handler is not declared",
		)
	}
	return selected(ctx, params.Invocation)
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
	handle      handler
}

func handlerRegistrations() []handlerRegistration {
	return []handlerRegistration{
		newHandlerRegistration(
			applicationHandlerID,
			sdk.ContributionApplication,
			"ApplicationHandler",
			ApplicationHandler,
		),
		newHandlerRegistration(
			serviceHandlerID,
			sdk.ContributionStereotype,
			"ServiceHandler",
			ServiceHandler,
		),
		newHandlerRegistration(
			beanHandlerID,
			sdk.ContributionProvider,
			"BeanHandler",
			BeanHandler,
		),
		newHandlerRegistration(
			configurationHandlerID,
			sdk.ContributionConfiguration,
			"ConfigurationHandler",
			ConfigurationHandler,
		),
		newHandlerRegistration(
			controllerHandlerID,
			sdk.ContributionController,
			"ControllerHandler",
			ControllerHandler,
		),
		newHandlerRegistration(
			getHandlerID,
			sdk.ContributionRoute,
			"GetHandler",
			GetHandler,
		),
		newHandlerRegistration(
			postHandlerID,
			sdk.ContributionRoute,
			"PostHandler",
			PostHandler,
		),
		newHandlerRegistration(
			moduleHandlerID,
			sdk.ContributionModule,
			"ModuleHandler",
			ModuleHandler,
		),
		newHandlerRegistration(
			namedInterfaceHandlerID,
			sdk.ContributionNamedInterface,
			"NamedInterfaceHandler",
			NamedInterfaceHandler,
		),
		newHandlerRegistration(
			onStartHandlerID,
			sdk.ContributionLifecycle,
			"OnStartHandler",
			OnStartHandler,
		),
		newHandlerRegistration(
			onStopHandlerID,
			sdk.ContributionLifecycle,
			"OnStopHandler",
			OnStopHandler,
		),
		newHandlerRegistration(
			asyncExecuteHandlerID,
			sdk.ContributionAsync,
			"AsyncExecuteHandler",
			AsyncExecuteHandler,
		),
		newHandlerRegistration(
			cacheableHandlerID,
			sdk.ContributionCache,
			"CacheableHandler",
			CacheableHandler,
		),
		newHandlerRegistration(
			transactionalHandlerID,
			sdk.ContributionTransaction,
			"TransactionalHandler",
			TransactionalHandler,
		),
		newHandlerRegistration(
			eventTopicHandlerID,
			sdk.ContributionEventTopic,
			"EventTopicHandler",
			EventTopicHandler,
		),
		newHandlerRegistration(
			eventListenerHandlerID,
			sdk.ContributionEventListener,
			"EventListenerHandler",
			EventListenerHandler,
		),
		newHandlerRegistration(
			fixedDelayHandlerID,
			sdk.ContributionSchedule,
			"FixedDelayHandler",
			FixedDelayHandler,
		),
		newHandlerRegistration(
			authorizeHandlerID,
			sdk.ContributionAuthorization,
			"AuthorizeHandler",
			AuthorizeHandler,
		),
		newHandlerRegistration(
			managementEnableHandlerID,
			sdk.ContributionBootstrap,
			"ManagementEnableHandler",
			ManagementEnableHandler,
		),
		newHandlerRegistration(
			observabilityLoggingHandlerID,
			sdk.ContributionBootstrap,
			"ObservabilityLoggingHandler",
			ObservabilityLoggingHandler,
		),
	}
}

func newHandlerRegistration(
	id string,
	capability sdk.ContributionKind,
	name string,
	handle handler,
) handlerRegistration {
	return handlerRegistration{
		description: protocol.Handler{
			ID:           id,
			Capabilities: []string{string(capability)},
			Source: sdk.Symbol{
				Package: "github.com/StevenBuglione/spice/internal/annotationcore",
				Name:    name,
			},
		},
		handle: handle,
	}
}
