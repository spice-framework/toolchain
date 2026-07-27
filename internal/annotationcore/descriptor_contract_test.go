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
	definitions := []sdk.Definition{
		asyncannotation.Execute(),
		cacheannotation.Cacheable(),
		coreannotation.Application(),
		coreannotation.Bean(),
		coreannotation.Configuration(),
		coreannotation.Service(),
		dataannotation.Transactional(),
		eventannotation.Listener(),
		eventannotation.Topic(),
		lifecycleannotation.OnStart(),
		lifecycleannotation.OnStop(),
		managementannotation.Enable(),
		modulithannotation.Module(),
		modulithannotation.NamedInterface(),
		observabilityannotation.Logging(),
		scheduleannotation.FixedDelay(),
		securityannotation.Authorize(),
		webannotation.Controller(),
		webannotation.Get(),
		webannotation.Post(),
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
		if _, duplicate := handlers[handler.ID]; duplicate {
			t.Fatalf("Describe() repeats handler %q", handler.ID)
		}
		handlers[handler.ID] = handler
	}
	if len(handlers) != len(definitions) {
		t.Fatalf(
			"Describe() handlers = %d, official descriptors = %d",
			len(handlers),
			len(definitions),
		)
	}
	for _, definition := range definitions {
		if err := definition.Validate(); err != nil {
			t.Fatalf("descriptor %q: %v", definition.Name, err)
		}
		handler, found := handlers[definition.Implementation.Handler]
		if !found {
			t.Fatalf(
				"descriptor %q handler %q is not declared",
				definition.Name,
				definition.Implementation.Handler,
			)
		}
		if handler.Source != definition.Implementation.Source {
			t.Fatalf(
				"descriptor %q source = %+v, handler source = %+v",
				definition.Name,
				definition.Implementation.Source,
				handler.Source,
			)
		}
	}
}
