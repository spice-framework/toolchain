package generate

import (
	"bytes"
	"fmt"
	"testing"
)

func TestRenderGeneratesExecutableTypedEventTopics(t *testing.T) {
	root := eventGenerationFixture(t, 20)
	program, model, applicationTarget := buildApplication(t, root, "./...")
	target, diagnostics := DefaultTarget(program, applicationTarget)
	if len(diagnostics) != 0 {
		t.Fatalf(
			"DefaultTarget() diagnostics = %v",
			generationDiagnosticStrings(diagnostics),
		)
	}

	var firstPlan Plan
	var firstSource, firstManifest []byte
	for iteration := range 10 {
		plan, renderDiagnostics := Render(
			program,
			model,
			applicationTarget,
			target,
		)
		if len(renderDiagnostics) != 0 {
			t.Fatalf(
				"Render() diagnostics = %v",
				generationDiagnosticStrings(renderDiagnostics),
			)
		}
		source := generatedGoContent(t, plan)
		manifest := plan.ManifestContent()
		if iteration == 0 {
			firstPlan = plan
			firstSource = source
			firstManifest = manifest
			writePlan(t, root, plan)
			continue
		}
		if !bytes.Equal(firstSource, source) ||
			!bytes.Equal(firstManifest, manifest) {
			t.Fatalf("event generation changed bytes at iteration %d", iteration)
		}
	}

	for _, required := range []string{
		`spiceevent "github.com/StevenBuglione/spice/event"`,
		"EventObservers",
		"[]spiceevent.Observer",
		"spiceevent.NewTopic(",
		`ID:     "spice:symbol:v1|function|33:example.com/eventgenerated/events|0:|11:OrderEvents"`,
		`Module: "example.com/eventgenerated/events"`,
		"[]spiceevent.Subscriber[events.OrderPlaced]",
		`ID:     "spice:symbol:v1|method|33:example.com/eventgenerated/events|9:Inventory|7:Reserve"`,
		"Order:  -10",
		"Handle: inventory.Reserve",
		`ID:     "spice:symbol:v1|method|33:example.com/eventgenerated/events|5:Audit|6:Record"`,
		"Order:  20",
		"Handle: audit.Record",
		"options.EventObservers...",
		"var orderEvents spiceevent.Publisher[events.OrderPlaced]",
	} {
		if !bytes.Contains(firstSource, []byte(required)) {
			t.Fatalf("generated event source missing %q:\n%s", required, firstSource)
		}
	}
	assertOrdered(
		t,
		string(generatedFileContent(
			t,
			firstPlan,
			"internal/spicegen/eventgenerated/"+providersFilename,
		)),
		"inventory,",
		"audit,",
		"spiceevent.NewTopic(",
		"service,",
	)
	eventSource := generatedSourceUnitContent(t, firstPlan, "events/events.go")
	for _, directCall := range [][]byte{
		[]byte("events.NewInventory()"),
		[]byte("events.NewAudit()"),
		[]byte("events.NewService(dependency0)"),
	} {
		if !bytes.Contains(eventSource, directCall) {
			t.Fatalf(
				"generated event source unit omitted %q:\n%s",
				directCall,
				eventSource,
			)
		}
	}
	assertOrdered(
		t,
		string(generatedFileContent(
			t,
			firstPlan,
			"internal/spicegen/eventgenerated/"+providersFilename,
		)),
		`ID:     "spice:symbol:v1|method|33:example.com/eventgenerated/events|9:Inventory|7:Reserve"`,
		`ID:     "spice:symbol:v1|method|33:example.com/eventgenerated/events|5:Audit|6:Record"`,
	)

	writeTestFile(
		t,
		root,
		"internal/spicegen/eventgenerated/zz_spice_event_test.go",
		generatedEventTest,
	)
	runGoTest(t, root, "./internal/spicegen/eventgenerated")
}

func TestEventMetadataChangesManifestInputHash(t *testing.T) {
	first := renderEventFixture(t, eventGenerationFixture(t, 20))
	second := renderEventFixture(t, eventGenerationFixture(t, 21))
	if first.Manifest().InputSHA256 == second.Manifest().InputSHA256 {
		t.Fatal("listener order changed generated behavior without changing input hash")
	}
}

func renderEventFixture(t *testing.T, root string) Plan {
	t.Helper()
	program, model, applicationTarget := buildApplication(t, root, "./...")
	target, diagnostics := DefaultTarget(program, applicationTarget)
	if len(diagnostics) != 0 {
		t.Fatalf(
			"DefaultTarget() diagnostics = %v",
			generationDiagnosticStrings(diagnostics),
		)
	}
	plan, diagnostics := Render(program, model, applicationTarget, target)
	if len(diagnostics) != 0 {
		t.Fatalf(
			"Render() diagnostics = %v",
			generationDiagnosticStrings(diagnostics),
		)
	}
	return plan
}

func eventGenerationFixture(t *testing.T, auditOrder int) string {
	t.Helper()
	return writeModule(t, "example.com/eventgenerated", map[string]string{
		"events/events.go": `// Package events owns the order event contract.
//
// @Module
package events

import (
	"context"
	"sync"

	"github.com/StevenBuglione/spice/event"
	"github.com/StevenBuglione/spice/lifecycle"
)

var state struct {
	sync.Mutex
	trace   []string
	current *Service
}

type OrderPlaced struct {
	ID string
}

type Audit struct{}

// @Bean
func NewAudit() (*Audit, lifecycle.Cleanup) {
	appendTrace("construct audit")
	return &Audit{}, func(context.Context) error {
		appendTrace("cleanup audit")
		return nil
	}
}

// @event.Listener(order=` + fmt.Sprint(auditOrder) + `)
func (*Audit) Record(_ context.Context, placed OrderPlaced) error {
	appendTrace("audit " + placed.ID)
	return nil
}

type Inventory struct{}

// @Bean
func NewInventory() (*Inventory, lifecycle.Cleanup) {
	appendTrace("construct inventory")
	return &Inventory{}, func(context.Context) error {
		appendTrace("cleanup inventory")
		return nil
	}
}

// @event.Listener(order=-10)
func (*Inventory) Reserve(_ context.Context, placed OrderPlaced) error {
	appendTrace("inventory " + placed.ID)
	return nil
}

// @event.Topic
func OrderEvents(*Audit, *Inventory) event.Publisher[OrderPlaced] {
	panic("event topic marker bodies must never execute")
}

type Service struct {
	publisher event.Publisher[OrderPlaced]
}

// @Bean
func NewService(publisher event.Publisher[OrderPlaced]) *Service {
	service := &Service{publisher: publisher}
	state.Lock()
	state.current = service
	state.Unlock()
	return service
}

func (service *Service) Place(ctx context.Context, id string) error {
	return service.publisher.Publish(ctx, OrderPlaced{ID: id})
}

func Current() *Service {
	state.Lock()
	defer state.Unlock()
	return state.current
}

func Reset() {
	state.Lock()
	defer state.Unlock()
	state.trace = nil
	state.current = nil
}

func ClearTrace() {
	state.Lock()
	defer state.Unlock()
	state.trace = nil
}

func Trace() []string {
	state.Lock()
	defer state.Unlock()
	return append([]string(nil), state.trace...)
}

func appendTrace(value string) {
	state.Lock()
	defer state.Unlock()
	state.trace = append(state.trace, value)
}
`,
		"bootstrap/application.go": `package bootstrap

import "example.com/eventgenerated/events"

// @Application
func EventGenerated(*events.Service) {
	panic("application marker bodies must never execute")
}
`,
	})
}

const generatedEventTest = `package spicegen

import (
	"context"
	"strings"
	"testing"

	"example.com/eventgenerated/events"
	spiceevent "github.com/StevenBuglione/spice/event"
)

type recordingEventObserver struct {
	trace []string
}

func (observer *recordingEventObserver) BeginEvent(
	ctx context.Context,
	interaction spiceevent.Interaction,
) (context.Context, func(spiceevent.Result)) {
	observer.trace = append(observer.trace, "begin "+interaction.Subscriber.ID)
	return ctx, func(result spiceevent.Result) {
		if result.Err != nil || result.Panicked {
			observer.trace = append(observer.trace, "failed "+result.Interaction.Subscriber.ID)
			return
		}
		observer.trace = append(observer.trace, "end "+result.Interaction.Subscriber.ID)
	}
}

func TestGeneratedTypedEvents(t *testing.T) {
	events.Reset()
	application, err := NewApplicationWithOptions(
		context.Background(),
		ApplicationOptions{EventObservers: []spiceevent.Observer{nil}},
	)
	if err == nil || application != nil ||
		!strings.Contains(err.Error(), "observer 0 is nil") {
		t.Fatalf("nil observer construction = (%v, %v)", application, err)
	}
	if got, want := events.Trace(), []string{
		"construct inventory",
		"construct audit",
		"cleanup audit",
		"cleanup inventory",
	}; !equalStrings(got, want) {
		t.Fatalf("rollback trace = %v, want %v", got, want)
	}

	events.Reset()
	observer := &recordingEventObserver{}
	application, err = NewApplicationWithOptions(
		context.Background(),
		ApplicationOptions{
			EventObservers: []spiceevent.Observer{observer},
		},
	)
	if err != nil || application == nil {
		t.Fatalf("NewApplicationWithOptions() = (%v, %v)", application, err)
	}
	service := events.Current()
	if service == nil {
		t.Fatal("generated application did not construct the service root")
	}
	events.ClearTrace()
	if err := service.Place(context.Background(), "order-7"); err != nil {
		t.Fatalf("Place() error = %v", err)
	}
	if got, want := events.Trace(), []string{
		"inventory order-7",
		"audit order-7",
	}; !equalStrings(got, want) {
		t.Fatalf("listener trace = %v, want %v", got, want)
	}
	if got, want := observer.trace, []string{
		"begin spice:symbol:v1|method|33:example.com/eventgenerated/events|9:Inventory|7:Reserve",
		"end spice:symbol:v1|method|33:example.com/eventgenerated/events|9:Inventory|7:Reserve",
		"begin spice:symbol:v1|method|33:example.com/eventgenerated/events|5:Audit|6:Record",
		"end spice:symbol:v1|method|33:example.com/eventgenerated/events|5:Audit|6:Record",
	}; !equalStrings(got, want) {
		t.Fatalf("observer trace = %v, want %v", got, want)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := service.Place(cancelled, "cancelled"); err == nil {
		t.Fatal("cancelled publication succeeded")
	}
	if got, want := events.Trace(), []string{
		"inventory order-7",
		"audit order-7",
	}; !equalStrings(got, want) {
		t.Fatalf("cancelled publication reached a listener: %v", got)
	}

	if err := application.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
`
