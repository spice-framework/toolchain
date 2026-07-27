package annotationcore_test

import (
	"context"
	"testing"

	asyncannotation "github.com/StevenBuglione/spice/annotation/async"
	cacheannotation "github.com/StevenBuglione/spice/annotation/cache"
	coreannotation "github.com/StevenBuglione/spice/annotation/core"
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
	"github.com/StevenBuglione/spice/internal/annotationcore"
)

func TestEveryOfficialDescriptorHasOneDeclaredToolHandler(t *testing.T) {
	t.Parallel()
	definitions := []struct {
		descriptor sdk.Symbol
		definition sdk.Definition
	}{
		{sdk.Symbol{Package: "github.com/StevenBuglione/spice/annotation/async", Name: "Execute"}, asyncannotation.Execute()},
		{sdk.Symbol{Package: "github.com/StevenBuglione/spice/annotation/cache", Name: "Cacheable"}, cacheannotation.Cacheable()},
		{sdk.Symbol{Package: "github.com/StevenBuglione/spice/annotation/core", Name: "Application"}, coreannotation.Application()},
		{sdk.Symbol{Package: "github.com/StevenBuglione/spice/annotation/core", Name: "Bean"}, coreannotation.Bean()},
		{sdk.Symbol{Package: "github.com/StevenBuglione/spice/annotation/core", Name: "Configuration"}, coreannotation.Configuration()},
		{sdk.Symbol{Package: "github.com/StevenBuglione/spice/annotation/core", Name: "Implements"}, coreannotation.Implements()},
		{sdk.Symbol{Package: "github.com/StevenBuglione/spice/annotation/core", Name: "Qualifier"}, coreannotation.Qualifier()},
		{sdk.Symbol{Package: "github.com/StevenBuglione/spice/annotation/core", Name: "Primary"}, coreannotation.Primary()},
		{sdk.Symbol{Package: "github.com/StevenBuglione/spice/annotation/core", Name: "Fallback"}, coreannotation.Fallback()},
		{sdk.Symbol{Package: "github.com/StevenBuglione/spice/annotation/core", Name: "Order"}, coreannotation.Order()},
		{sdk.Symbol{Package: "github.com/StevenBuglione/spice/annotation/core", Name: "Singleton"}, coreannotation.Singleton()},
		{sdk.Symbol{Package: "github.com/StevenBuglione/spice/annotation/core", Name: "Prototype"}, coreannotation.Prototype()},
		{sdk.Symbol{Package: "github.com/StevenBuglione/spice/annotation/core", Name: "RequestScope"}, coreannotation.RequestScope()},
		{sdk.Symbol{Package: "github.com/StevenBuglione/spice/annotation/core", Name: "SessionScope"}, coreannotation.SessionScope()},
		{sdk.Symbol{Package: "github.com/StevenBuglione/spice/annotation/core", Name: "Repository"}, coreannotation.Repository()},
		{sdk.Symbol{Package: "github.com/StevenBuglione/spice/annotation/core", Name: "Service"}, coreannotation.Service()},
		{sdk.Symbol{Package: "github.com/StevenBuglione/spice/annotation/data", Name: "Transactional"}, dataannotation.Transactional()},
		{sdk.Symbol{Package: "github.com/StevenBuglione/spice/annotation/event", Name: "Listener"}, eventannotation.Listener()},
		{sdk.Symbol{Package: "github.com/StevenBuglione/spice/annotation/event", Name: "Topic"}, eventannotation.Topic()},
		{sdk.Symbol{Package: "github.com/StevenBuglione/spice/annotation/lifecycle", Name: "OnStart"}, lifecycleannotation.OnStart()},
		{sdk.Symbol{Package: "github.com/StevenBuglione/spice/annotation/lifecycle", Name: "OnStop"}, lifecycleannotation.OnStop()},
		{sdk.Symbol{Package: "github.com/StevenBuglione/spice/annotation/management", Name: "Enable"}, managementannotation.Enable()},
		{sdk.Symbol{Package: "github.com/StevenBuglione/spice/annotation/modulith", Name: "Module"}, modulithannotation.Module()},
		{sdk.Symbol{Package: "github.com/StevenBuglione/spice/annotation/modulith", Name: "NamedInterface"}, modulithannotation.NamedInterface()},
		{sdk.Symbol{Package: "github.com/StevenBuglione/spice/annotation/observability", Name: "Logging"}, observabilityannotation.Logging()},
		{sdk.Symbol{Package: "github.com/StevenBuglione/spice/annotation/schedule", Name: "FixedDelay"}, scheduleannotation.FixedDelay()},
		{sdk.Symbol{Package: "github.com/StevenBuglione/spice/annotation/security", Name: "Authorize"}, securityannotation.Authorize()},
		{sdk.Symbol{Package: "github.com/StevenBuglione/spice/annotation/web", Name: "Controller"}, webannotation.Controller()},
		{sdk.Symbol{Package: "github.com/StevenBuglione/spice/annotation/web", Name: "Get"}, webannotation.Get()},
		{sdk.Symbol{Package: "github.com/StevenBuglione/spice/annotation/web", Name: "Post"}, webannotation.Post()},
	}
	described, err := annotationcore.New().Describe(
		context.Background(),
		protocol.DescribeParams{},
	)
	if err != nil {
		t.Fatalf("Describe() error = %v", err)
	}
	handlers := make(map[string]protocol.Handler, len(described.Handlers))
	for _, handler := range described.Handlers {
		key := handler.Descriptor.Package + "." + handler.Descriptor.Name
		if _, duplicate := handlers[key]; duplicate {
			t.Fatalf("Describe() repeats descriptor %q", key)
		}
		handlers[key] = handler
	}
	if len(handlers) != len(definitions) {
		t.Fatalf(
			"Describe() handlers = %d, official descriptors = %d",
			len(handlers),
			len(definitions),
		)
	}
	for _, item := range definitions {
		if err := item.definition.Validate(); err != nil {
			t.Fatalf("descriptor %q: %v", item.definition.Name, err)
		}
		key := item.descriptor.Package + "." + item.descriptor.Name
		_, found := handlers[key]
		if !found {
			t.Fatalf(
				"descriptor %q is not registered",
				key,
			)
		}
	}
}
