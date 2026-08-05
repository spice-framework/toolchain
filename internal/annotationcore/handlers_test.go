package annotationcore

import (
	"context"
	"encoding/json"
	"testing"

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
	securityannotation "github.com/spice-framework/spice/annotation/security"
	webannotation "github.com/spice-framework/spice/annotation/web"
)

func TestOfficialHandlersReturnTypedContributions(t *testing.T) {
	t.Parallel()
	tests := []handlerTestCase{
		handlerCase("application", coreannotation.ApplicationHandler, "core", "Application", sdk.ContributionApplication),
		handlerCase("bean", coreannotation.BeanHandler, "core", "Bean", sdk.ContributionProvider),
		handlerCaseWithArguments("implements", coreannotation.ImplementsHandler, "core", "Implements", sdk.ContributionInterface, positionalIdentifierToolArgument("payments.Processor")),
		handlerCaseWithArguments("qualifier", coreannotation.QualifierHandler, "core", "Qualifier", sdk.ContributionBeanMetadata, positionalToolArgument("stripe")),
		handlerCase("primary", coreannotation.PrimaryHandler, "core", "Primary", sdk.ContributionBeanMetadata),
		handlerCase("fallback", coreannotation.FallbackHandler, "core", "Fallback", sdk.ContributionBeanMetadata),
		handlerCaseWithArguments("order", coreannotation.OrderHandler, "core", "Order", sdk.ContributionBeanMetadata, positionalIntegerToolArgument(-10)),
		handlerCase("singleton", coreannotation.SingletonHandler, "core", "Singleton", sdk.ContributionBeanMetadata),
		handlerCase("prototype", coreannotation.PrototypeHandler, "core", "Prototype", sdk.ContributionBeanMetadata),
		handlerCase("request scope", coreannotation.RequestScopeHandler, "core", "RequestScope", sdk.ContributionBeanMetadata),
		handlerCase("session scope", coreannotation.SessionScopeHandler, "core", "SessionScope", sdk.ContributionBeanMetadata),
		handlerCase("repository", coreannotation.RepositoryHandler, "core", "Repository", sdk.ContributionStereotype),
		handlerCase("service", coreannotation.ServiceHandler, "core", "Service", sdk.ContributionStereotype),
		handlerCaseWithArguments("configuration", coreannotation.ConfigurationHandler, "core", "Configuration", sdk.ContributionConfiguration, stringToolArgument("prefix", "server")),
		handlerCaseWithArguments("controller", webannotation.ControllerHandler, "web", "Controller", sdk.ContributionController, stringToolArgument("prefix", "/api")),
		handlerCaseWithArguments("get", webannotation.GetHandler, "web", "Get", sdk.ContributionRoute, positionalToolArgument("/orders")),
		handlerCaseWithArguments("post", webannotation.PostHandler, "web", "Post", sdk.ContributionRoute, stringToolArgument("path", "/orders")),
		handlerCaseWithArguments("module", modulithannotation.ModuleHandler, "modulith", "Module", sdk.ContributionModule, toolArgument("allowedDependencies", sdk.KindList, []string{"example.com/inventory"})),
		handlerCaseWithArguments("named interface", modulithannotation.NamedInterfaceHandler, "modulith", "NamedInterface", sdk.ContributionNamedInterface, positionalToolArgument("api")),
		handlerCase("on start", lifecycleannotation.OnStartHandler, "lifecycle", "OnStart", sdk.ContributionLifecycle),
		handlerCase("on stop", lifecycleannotation.OnStopHandler, "lifecycle", "OnStop", sdk.ContributionLifecycle),
		handlerCase("async", asyncannotation.AsyncExecuteHandler, "async", "Execute", sdk.ContributionAsync),
		handlerCaseWithArguments("cache", cacheannotation.CacheableHandler, "cache", "Cacheable", sdk.ContributionCache, stringToolArgument("name", "orders.by-id")),
		handlerCaseWithArguments("transaction", dataannotation.TransactionalHandler, "data", "Transactional", sdk.ContributionTransaction, stringToolArgument("isolation", "serializable"), toolArgument("readOnly", sdk.KindBoolean, true)),
		handlerCase("event topic", eventannotation.EventTopicHandler, "event", "Topic", sdk.ContributionEventTopic),
		handlerCaseWithArguments("event listener", eventannotation.EventListenerHandler, "event", "Listener", sdk.ContributionEventListener, toolArgument("order", sdk.KindInteger, int64(3))),
		handlerCaseWithArguments("fixed delay", scheduleannotation.FixedDelayHandler, "schedule", "FixedDelay", sdk.ContributionSchedule, stringToolArgument("delay", "5m"), stringToolArgument("initialDelay", "1s"), toolArgument("continueOnError", sdk.KindBoolean, true)),
		handlerCaseWithArguments("authorize", securityannotation.AuthorizeHandler, "security", "Authorize", sdk.ContributionAuthorization, toolArgument("authenticated", sdk.KindBoolean, true), toolArgument("anyRoles", sdk.KindList, []string{"operator"}), toolArgument("allRoles", sdk.KindList, []string{"member"}), toolArgument("allScopes", sdk.KindList, []string{"orders.read"})),
		handlerCaseWithArguments("management", managementannotation.ManagementEnableHandler, "management", "Enable", sdk.ContributionBootstrap, toolArgument("expose", sdk.KindList, []string{"health", "info"})),
		handlerCase("logging", observabilityannotation.ObservabilityLoggingHandler, "observability", "Logging", sdk.ContributionBootstrap),
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result, err := test.handle(t.Context(), test.invocation)
			if err != nil {
				t.Fatalf("handler error = %v", err)
			}
			if len(result.Contributions) == 0 ||
				result.Contributions[0].Kind != test.kind {
				t.Fatalf(
					"contributions = %+v, want one %q",
					result.Contributions,
					test.kind,
				)
			}
		})
	}
}

func TestHandlerArgumentValidationFailsClosed(t *testing.T) {
	t.Parallel()
	tests := []handlerFailureTestCase{
		{
			name:   "descriptor identity",
			handle: coreannotation.ApplicationHandler,
			invocation: sdk.Invocation{
				DescriptorPackage: "example.com/wrong",
				DescriptorSymbol:  "Application",
			},
		},
		handlerFailureCase("unsupported positional", coreannotation.BeanHandler, "core", "Bean", positionalToolArgument("value")),
		handlerFailureCase("unnamed non-positional", coreannotation.ConfigurationHandler, "core", "Configuration", toolArgument("", sdk.KindString, "server")),
		handlerFailureCase("unsupported name", coreannotation.ConfigurationHandler, "core", "Configuration", stringToolArgument("unknown", "server")),
		handlerFailureCase("duplicate", coreannotation.ConfigurationHandler, "core", "Configuration", stringToolArgument("prefix", "one"), stringToolArgument("prefix", "two")),
		handlerFailureCase("required missing", cacheannotation.CacheableHandler, "cache", "Cacheable"),
		handlerFailureCase("required empty", cacheannotation.CacheableHandler, "cache", "Cacheable", stringToolArgument("name", " ")),
		handlerFailureCase("string kind", cacheannotation.CacheableHandler, "cache", "Cacheable", toolArgument("name", sdk.KindBoolean, true)),
		handlerFailureCase("string JSON", cacheannotation.CacheableHandler, "cache", "Cacheable", malformedToolArgument("name", sdk.KindString)),
		handlerFailureCase("list kind", modulithannotation.ModuleHandler, "modulith", "Module", stringToolArgument("allowedDependencies", "wrong")),
		handlerFailureCase("list JSON", modulithannotation.ModuleHandler, "modulith", "Module", malformedToolArgument("allowedDependencies", sdk.KindList)),
		handlerFailureCase("boolean kind", dataannotation.TransactionalHandler, "data", "Transactional", stringToolArgument("readOnly", "wrong")),
		handlerFailureCase("boolean JSON", dataannotation.TransactionalHandler, "data", "Transactional", malformedToolArgument("readOnly", sdk.KindBoolean)),
		handlerFailureCase("integer kind", eventannotation.EventListenerHandler, "event", "Listener", stringToolArgument("order", "wrong")),
		handlerFailureCase("integer JSON", eventannotation.EventListenerHandler, "event", "Listener", malformedToolArgument("order", sdk.KindInteger)),
		handlerFailureCase("management expose", managementannotation.ManagementEnableHandler, "management", "Enable"),
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

type handlerTestCase struct {
	name       string
	handle     sdk.Handler
	invocation sdk.Invocation
	kind       sdk.ContributionKind
}

type handlerFailureTestCase struct {
	name       string
	handle     sdk.Handler
	invocation sdk.Invocation
}

func handlerCase(
	name string,
	handle sdk.Handler,
	domain string,
	symbol string,
	kind sdk.ContributionKind,
) handlerTestCase {
	return handlerCaseWithArguments(name, handle, domain, symbol, kind)
}

func handlerCaseWithArguments(
	name string,
	handle sdk.Handler,
	domain string,
	symbol string,
	kind sdk.ContributionKind,
	arguments ...sdk.InvocationArgument,
) handlerTestCase {
	return handlerTestCase{
		name:   name,
		handle: handle,
		invocation: sdk.Invocation{
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
	handle sdk.Handler,
	domain string,
	symbol string,
	arguments ...sdk.InvocationArgument,
) handlerFailureTestCase {
	return handlerFailureTestCase{
		name:   name,
		handle: handle,
		invocation: sdk.Invocation{
			DescriptorPackage: annotationPackage(domain),
			DescriptorSymbol:  symbol,
			CanonicalName:     domain + "." + symbol,
			Arguments:         arguments,
		},
	}
}

func annotationPackage(domain string) string {
	return "github.com/spice-framework/spice/annotation/" + domain
}

func positionalToolArgument(value string) sdk.InvocationArgument {
	argument := stringToolArgument("", value)
	argument.Positional = true
	return argument
}

func positionalIdentifierToolArgument(value string) sdk.InvocationArgument {
	argument := toolArgument("", sdk.KindIdentifier, value)
	argument.Positional = true
	return argument
}

func positionalIntegerToolArgument(value int64) sdk.InvocationArgument {
	argument := toolArgument("", sdk.KindInteger, value)
	argument.Positional = true
	return argument
}

func stringToolArgument(
	name string,
	value string,
) sdk.InvocationArgument {
	return toolArgument(name, sdk.KindString, value)
}

func toolArgument(
	name string,
	kind sdk.Kind,
	value any,
) sdk.InvocationArgument {
	content, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return sdk.InvocationArgument{Name: name, Kind: kind, Value: content}
}

func malformedToolArgument(
	name string,
	kind sdk.Kind,
) sdk.InvocationArgument {
	return sdk.InvocationArgument{
		Name:  name,
		Kind:  kind,
		Value: json.RawMessage(`{`),
	}
}
