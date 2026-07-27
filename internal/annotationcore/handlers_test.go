package annotationcore

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/StevenBuglione/spice/annotation/sdk"
	"github.com/StevenBuglione/spice/annotation/sdk/protocol"
)

func TestOfficialHandlersReturnTypedContributions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		handle     handler
		invocation protocol.Invocation
		kind       sdk.ContributionKind
	}{
		handlerCase("application", ApplicationHandler, "core", "Application", sdk.ContributionApplication),
		handlerCase("bean", BeanHandler, "core", "Bean", sdk.ContributionProvider),
		handlerCase("service", ServiceHandler, "core", "Service", sdk.ContributionStereotype),
		handlerCaseWithArguments("configuration", ConfigurationHandler, "core", "Configuration", sdk.ContributionConfiguration, stringToolArgument("prefix", "server")),
		handlerCaseWithArguments("controller", ControllerHandler, "web", "Controller", sdk.ContributionController, stringToolArgument("prefix", "/api")),
		handlerCaseWithArguments("get", GetHandler, "web", "Get", sdk.ContributionRoute, positionalToolArgument("/orders")),
		handlerCaseWithArguments("post", PostHandler, "web", "Post", sdk.ContributionRoute, stringToolArgument("path", "/orders")),
		handlerCaseWithArguments("module", ModuleHandler, "modulith", "Module", sdk.ContributionModule, toolArgument("allowedDependencies", sdk.KindList, []string{"example.com/inventory"})),
		handlerCaseWithArguments("named interface", NamedInterfaceHandler, "modulith", "NamedInterface", sdk.ContributionNamedInterface, positionalToolArgument("api")),
		handlerCase("on start", OnStartHandler, "lifecycle", "OnStart", sdk.ContributionLifecycle),
		handlerCase("on stop", OnStopHandler, "lifecycle", "OnStop", sdk.ContributionLifecycle),
		handlerCase("async", AsyncExecuteHandler, "async", "Execute", sdk.ContributionAsync),
		handlerCaseWithArguments("cache", CacheableHandler, "cache", "Cacheable", sdk.ContributionCache, stringToolArgument("name", "orders.by-id")),
		handlerCaseWithArguments("transaction", TransactionalHandler, "data", "Transactional", sdk.ContributionTransaction, stringToolArgument("isolation", "serializable"), toolArgument("readOnly", sdk.KindBoolean, true)),
		handlerCase("event topic", EventTopicHandler, "event", "Topic", sdk.ContributionEventTopic),
		handlerCaseWithArguments("event listener", EventListenerHandler, "event", "Listener", sdk.ContributionEventListener, toolArgument("order", sdk.KindInteger, int64(3))),
		handlerCaseWithArguments("fixed delay", FixedDelayHandler, "schedule", "FixedDelay", sdk.ContributionSchedule, stringToolArgument("delay", "5m"), stringToolArgument("initialDelay", "1s"), toolArgument("continueOnError", sdk.KindBoolean, true)),
		handlerCaseWithArguments("authorize", AuthorizeHandler, "security", "Authorize", sdk.ContributionAuthorization, toolArgument("authenticated", sdk.KindBoolean, true), toolArgument("anyRoles", sdk.KindList, []string{"operator"}), toolArgument("allRoles", sdk.KindList, []string{"member"}), toolArgument("allScopes", sdk.KindList, []string{"orders.read"})),
		handlerCaseWithArguments("management", ManagementEnableHandler, "management", "Enable", sdk.ContributionBootstrap, toolArgument("expose", sdk.KindList, []string{"health", "info"})),
		handlerCase("logging", ObservabilityLoggingHandler, "observability", "Logging", sdk.ContributionBootstrap),
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result, err := test.handle(t.Context(), test.invocation)
			if err != nil {
				t.Fatalf("handler error = %v", err)
			}
			if len(result.Contributions) != 1 {
				t.Fatalf("contributions = %+v", result.Contributions)
			}
			contribution, err := protocol.DecodeContribution(
				result.Contributions[0],
			)
			if err != nil {
				t.Fatalf("DecodeContribution() error = %v", err)
			}
			if contribution.Kind != test.kind {
				t.Fatalf(
					"contribution kind = %q, want %q",
					contribution.Kind,
					test.kind,
				)
			}
		})
	}
}

func TestHandlerArgumentValidationFailsClosed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		handle     handler
		invocation protocol.Invocation
	}{
		{
			name:   "descriptor identity",
			handle: ApplicationHandler,
			invocation: protocol.Invocation{
				DescriptorPackage: "example.com/wrong",
				DescriptorSymbol:  "Application",
			},
		},
		handlerFailureCase("unsupported positional", BeanHandler, "core", "Bean", positionalToolArgument("value")),
		handlerFailureCase("unnamed non-positional", ConfigurationHandler, "core", "Configuration", toolArgument("", sdk.KindString, "server")),
		handlerFailureCase("unsupported name", ConfigurationHandler, "core", "Configuration", stringToolArgument("unknown", "server")),
		handlerFailureCase("duplicate", ConfigurationHandler, "core", "Configuration", stringToolArgument("prefix", "one"), stringToolArgument("prefix", "two")),
		handlerFailureCase("required missing", CacheableHandler, "cache", "Cacheable"),
		handlerFailureCase("required empty", CacheableHandler, "cache", "Cacheable", stringToolArgument("name", " ")),
		handlerFailureCase("string kind", CacheableHandler, "cache", "Cacheable", toolArgument("name", sdk.KindBoolean, true)),
		handlerFailureCase("string JSON", CacheableHandler, "cache", "Cacheable", malformedToolArgument("name", sdk.KindString)),
		handlerFailureCase("list kind", ModuleHandler, "modulith", "Module", stringToolArgument("allowedDependencies", "wrong")),
		handlerFailureCase("list JSON", ModuleHandler, "modulith", "Module", malformedToolArgument("allowedDependencies", sdk.KindList)),
		handlerFailureCase("boolean kind", TransactionalHandler, "data", "Transactional", stringToolArgument("readOnly", "wrong")),
		handlerFailureCase("boolean JSON", TransactionalHandler, "data", "Transactional", malformedToolArgument("readOnly", sdk.KindBoolean)),
		handlerFailureCase("integer kind", EventListenerHandler, "event", "Listener", stringToolArgument("order", "wrong")),
		handlerFailureCase("integer JSON", EventListenerHandler, "event", "Listener", malformedToolArgument("order", sdk.KindInteger)),
		handlerFailureCase("management expose", ManagementEnableHandler, "management", "Enable"),
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := test.handle(
				context.Background(),
				test.invocation,
			); err == nil {
				t.Fatal("handler error = nil, want failure")
			}
		})
	}
}

func handlerCase(
	name string,
	handle handler,
	domain string,
	symbol string,
	kind sdk.ContributionKind,
) struct {
	name       string
	handle     handler
	invocation protocol.Invocation
	kind       sdk.ContributionKind
} {
	return handlerCaseWithArguments(
		name,
		handle,
		domain,
		symbol,
		kind,
	)
}

func handlerCaseWithArguments(
	name string,
	handle handler,
	domain string,
	symbol string,
	kind sdk.ContributionKind,
	arguments ...protocol.Argument,
) struct {
	name       string
	handle     handler
	invocation protocol.Invocation
	kind       sdk.ContributionKind
} {
	return struct {
		name       string
		handle     handler
		invocation protocol.Invocation
		kind       sdk.ContributionKind
	}{
		name:   name,
		handle: handle,
		invocation: protocol.Invocation{
			DescriptorPackage: annotationPackage(domain),
			DescriptorSymbol:  symbol,
			CanonicalName:     domain + "." + symbol,
			Arguments:         arguments,
		},
		kind: kind,
	}
}

func handlerFailureCase(
	name string,
	handle handler,
	domain string,
	symbol string,
	arguments ...protocol.Argument,
) struct {
	name       string
	handle     handler
	invocation protocol.Invocation
} {
	return struct {
		name       string
		handle     handler
		invocation protocol.Invocation
	}{
		name:   name,
		handle: handle,
		invocation: protocol.Invocation{
			DescriptorPackage: annotationPackage(domain),
			DescriptorSymbol:  symbol,
			CanonicalName:     domain + "." + symbol,
			Arguments:         arguments,
		},
	}
}

func annotationPackage(domain string) string {
	return "github.com/StevenBuglione/spice/annotation/" + domain
}

func positionalToolArgument(value string) protocol.Argument {
	argument := stringToolArgument("", value)
	argument.Positional = true
	return argument
}

func stringToolArgument(name string, value string) protocol.Argument {
	return toolArgument(name, sdk.KindString, value)
}

func toolArgument(
	name string,
	kind sdk.Kind,
	value any,
) protocol.Argument {
	content, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return protocol.Argument{Name: name, Kind: kind, Value: content}
}

func malformedToolArgument(
	name string,
	kind sdk.Kind,
) protocol.Argument {
	return protocol.Argument{
		Name:  name,
		Kind:  kind,
		Value: json.RawMessage(`{`),
	}
}
