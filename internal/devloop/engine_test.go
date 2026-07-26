package devloop

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestEnginePreservesLastKnownGoodAndRecovers(t *testing.T) {
	t.Parallel()
	clock := newFakeClock(time.Unix(10_000, 0))
	watcher := newFakeWatcher()
	pipeline := newFakePipeline()
	launcher := newFakeLauncher()
	sink := newFakeSink()
	engine, err := NewEngine(
		Config{
			QuietPeriod: time.Second,
			MaxDelay:    3 * time.Second,
			StopTimeout: time.Second,
		},
		watcher,
		clock,
		pipeline,
		launcher,
		sink,
	)
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() {
		runDone <- engine.Run(ctx)
	}()

	initial := receivePreparation(t, pipeline.requests)
	if initial.batch.Revision() != 1 {
		t.Fatalf("initial revision = %d, want 1", initial.batch.Revision())
	}
	initialCandidate, initialDisposals := testCandidate(t, "initial")
	initial.respond <- preparationResponse{candidate: initialCandidate}
	waitForEvent(t, sink.events, EventApplicationStarted)
	firstProcess := receiveProcess(t, launcher.started)

	watcher.events <- FileEvent{Path: "orders/service.go", Kind: ChangeWrite}
	change := waitForEvent(t, sink.events, EventChangeDetected)
	if !change.Stale {
		t.Fatal("change Stale = false with active process, want true")
	}
	clock.Advance(time.Second)
	broken := receivePreparation(t, pipeline.requests)
	broken.respond <- preparationResponse{err: errors.New("compile failed")}
	failure := waitForEvent(t, sink.events, EventPreparationFailed)
	if failure.Revision != 2 {
		t.Fatalf("failure revision = %d, want 2", failure.Revision)
	}
	waitForEvent(t, sink.events, EventLastKnownGood)
	if firstProcess.stops.Load() != 0 {
		t.Fatalf("first process stops = %d after failed build, want 0", firstProcess.stops.Load())
	}

	watcher.events <- FileEvent{Path: "orders/service.go", Kind: ChangeWrite}
	waitForEvent(t, sink.events, EventChangeDetected)
	clock.Advance(time.Second)
	fixed := receivePreparation(t, pipeline.requests)
	fixedCandidate, fixedDisposals := testCandidate(t, "fixed")
	fixed.respond <- preparationResponse{candidate: fixedCandidate}
	waitForEvent(t, sink.events, EventRestartRequested)
	started := waitForEvent(t, sink.events, EventApplicationStarted)
	if started.Revision != 3 {
		t.Fatalf("started revision = %d, want 3", started.Revision)
	}
	secondProcess := receiveProcess(t, launcher.started)
	if firstProcess.stops.Load() != 1 {
		t.Fatalf("first process stops = %d after recovery, want 1", firstProcess.stops.Load())
	}
	if initialDisposals.Load() != 1 {
		t.Fatalf("initial candidate disposals = %d, want 1", initialDisposals.Load())
	}

	cancel()
	if err := receiveRunResult(t, runDone); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if secondProcess.stops.Load() != 1 {
		t.Fatalf("second process stops = %d at shutdown, want 1", secondProcess.stops.Load())
	}
	if fixedDisposals.Load() != 1 {
		t.Fatalf("fixed candidate disposals = %d, want 1", fixedDisposals.Load())
	}
	if watcher.closes.Load() != 1 {
		t.Fatalf("watcher closes = %d, want 1", watcher.closes.Load())
	}
}

func TestEngineRejectsObsoletePreparation(t *testing.T) {
	t.Parallel()
	clock := newFakeClock(time.Unix(20_000, 0))
	watcher := newFakeWatcher()
	pipeline := newFakePipeline()
	launcher := newFakeLauncher()
	sink := newFakeSink()
	engine, err := NewEngine(
		Config{
			QuietPeriod: time.Second,
			MaxDelay:    time.Second,
			StopTimeout: time.Second,
		},
		watcher,
		clock,
		pipeline,
		launcher,
		sink,
	)
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() {
		runDone <- engine.Run(ctx)
	}()

	obsolete := receivePreparation(t, pipeline.requests)
	watcher.events <- FileEvent{Path: "main.go", Kind: ChangeWrite}
	waitForEvent(t, sink.events, EventChangeDetected)
	obsoleteCandidate, obsoleteDisposals := testCandidate(t, "obsolete")
	obsolete.respond <- preparationResponse{candidate: obsoleteCandidate}
	discarded := waitForEvent(t, sink.events, EventCandidateDiscarded)
	if discarded.Revision != 1 {
		t.Fatalf("discarded revision = %d, want 1", discarded.Revision)
	}
	if obsoleteDisposals.Load() != 1 {
		t.Fatalf("obsolete candidate disposals = %d, want 1", obsoleteDisposals.Load())
	}

	clock.Advance(time.Second)
	current := receivePreparation(t, pipeline.requests)
	currentCandidate, _ := testCandidate(t, "current")
	current.respond <- preparationResponse{candidate: currentCandidate}
	waitForEvent(t, sink.events, EventApplicationStarted)
	receiveProcess(t, launcher.started)
	if launcher.startCount.Load() != 1 {
		t.Fatalf("launcher starts = %d, want 1", launcher.startCount.Load())
	}

	cancel()
	if err := receiveRunResult(t, runDone); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestEngineRecoversAfterInitialFailure(t *testing.T) {
	t.Parallel()
	clock := newFakeClock(time.Unix(30_000, 0))
	watcher := newFakeWatcher()
	pipeline := newFakePipeline()
	launcher := newFakeLauncher()
	sink := newFakeSink()
	engine, err := NewEngine(
		Config{
			QuietPeriod: time.Second,
			MaxDelay:    time.Second,
			StopTimeout: time.Second,
		},
		watcher,
		clock,
		pipeline,
		launcher,
		sink,
	)
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() {
		runDone <- engine.Run(ctx)
	}()

	initial := receivePreparation(t, pipeline.requests)
	initial.respond <- preparationResponse{err: errors.New("invalid annotation")}
	failed := waitForEvent(t, sink.events, EventPreparationFailed)
	if failed.Stale {
		t.Fatal("initial failure Stale = true, want false")
	}
	watcher.events <- FileEvent{Path: "main.go", Kind: ChangeWrite}
	waitForEvent(t, sink.events, EventChangeDetected)
	clock.Advance(time.Second)
	recovery := receivePreparation(t, pipeline.requests)
	candidate, _ := testCandidate(t, "recovered")
	recovery.respond <- preparationResponse{candidate: candidate}
	waitForEvent(t, sink.events, EventApplicationStarted)
	receiveProcess(t, launcher.started)

	cancel()
	if err := receiveRunResult(t, runDone); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestEngineReplacesAfterSuccessfulStopEscalation(t *testing.T) {
	t.Parallel()
	clock := newFakeClock(time.Unix(35_000, 0))
	watcher := newFakeWatcher()
	pipeline := newFakePipeline()
	launcher := newFakeLauncher()
	sink := newFakeSink()
	engine, err := NewEngine(
		Config{
			QuietPeriod: time.Second,
			MaxDelay:    time.Second,
			StopTimeout: time.Second,
		},
		watcher,
		clock,
		pipeline,
		launcher,
		sink,
	)
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() {
		runDone <- engine.Run(ctx)
	}()

	initial := receivePreparation(t, pipeline.requests)
	initialCandidate, _ := testCandidate(t, "initial")
	initial.respond <- preparationResponse{candidate: initialCandidate}
	waitForEvent(t, sink.events, EventApplicationStarted)
	firstProcess := receiveProcess(t, launcher.started)
	firstProcess.stopErr = ErrGracefulStopTimeout

	watcher.events <- FileEvent{Path: "main.go", Kind: ChangeWrite}
	waitForEvent(t, sink.events, EventChangeDetected)
	clock.Advance(time.Second)
	next := receivePreparation(t, pipeline.requests)
	nextCandidate, _ := testCandidate(t, "next")
	next.respond <- preparationResponse{candidate: nextCandidate}
	waitForEvent(t, sink.events, EventShutdownTimedOut)
	started := waitForEvent(t, sink.events, EventApplicationStarted)
	if started.Revision != 2 {
		t.Fatalf("started revision = %d, want 2", started.Revision)
	}
	receiveProcess(t, launcher.started)

	cancel()
	if err := receiveRunResult(t, runDone); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestEngineReportsWatcherClosure(t *testing.T) {
	t.Parallel()
	clock := newFakeClock(time.Unix(40_000, 0))
	watcher := newFakeWatcher()
	pipeline := newFakePipeline()
	launcher := newFakeLauncher()
	engine, err := NewEngine(
		Config{},
		watcher,
		clock,
		pipeline,
		launcher,
		nil,
	)
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}
	runDone := make(chan error, 1)
	go func() {
		runDone <- engine.Run(context.Background())
	}()
	receivePreparation(t, pipeline.requests)
	close(watcher.events)
	runErr := receiveRunResult(t, runDone)
	if !errors.Is(runErr, ErrWatcherClosed) {
		t.Fatalf("Run() error = %v, want ErrWatcherClosed", runErr)
	}
}

func TestNewEngineValidatesDependenciesAndBounds(t *testing.T) {
	t.Parallel()
	watcher := newFakeWatcher()
	pipeline := newFakePipeline()
	launcher := newFakeLauncher()
	tests := []struct {
		name     string
		config   Config
		watcher  Watcher
		pipeline Pipeline
		launcher Launcher
	}{
		{
			name:     "negative quiet",
			config:   Config{QuietPeriod: -time.Second},
			watcher:  watcher,
			pipeline: pipeline,
			launcher: launcher,
		},
		{
			name: "short maximum",
			config: Config{
				QuietPeriod: time.Second,
				MaxDelay:    time.Millisecond,
			},
			watcher:  watcher,
			pipeline: pipeline,
			launcher: launcher,
		},
		{
			name:     "negative stop",
			config:   Config{StopTimeout: -time.Second},
			watcher:  watcher,
			pipeline: pipeline,
			launcher: launcher,
		},
		{name: "nil watcher", pipeline: pipeline, launcher: launcher},
		{name: "nil pipeline", watcher: watcher, launcher: launcher},
		{name: "nil launcher", watcher: watcher, pipeline: pipeline},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewEngine(
				test.config,
				test.watcher,
				nil,
				test.pipeline,
				test.launcher,
				nil,
			); err == nil {
				t.Fatal("NewEngine() error = nil, want failure")
			}
		})
	}
}

func TestCandidateDisposeIsSharedAndIdempotent(t *testing.T) {
	t.Parallel()
	candidate, disposals := testCandidate(t, "candidate")
	copyOfCandidate := candidate
	const goroutines = 20
	var wait sync.WaitGroup
	wait.Add(goroutines)
	for range goroutines {
		go func() {
			defer wait.Done()
			if err := copyOfCandidate.Dispose(); err != nil {
				t.Errorf("Dispose() error = %v", err)
			}
		}()
	}
	wait.Wait()
	if disposals.Load() != 1 {
		t.Fatalf("disposals = %d, want 1", disposals.Load())
	}
}

type fakeWatcher struct {
	events chan FileEvent
	errors chan error
	closes atomic.Int64
}

func newFakeWatcher() *fakeWatcher {
	return &fakeWatcher{
		events: make(chan FileEvent, 16),
		errors: make(chan error, 16),
	}
}

func (watcher *fakeWatcher) Events() <-chan FileEvent {
	return watcher.events
}

func (watcher *fakeWatcher) Errors() <-chan error {
	return watcher.errors
}

func (watcher *fakeWatcher) Close() error {
	watcher.closes.Add(1)
	return nil
}

type preparationResponse struct {
	candidate Candidate
	err       error
}

type preparationRequest struct {
	batch   Batch
	respond chan preparationResponse
}

type fakePipeline struct {
	requests chan preparationRequest
}

func newFakePipeline() *fakePipeline {
	return &fakePipeline{requests: make(chan preparationRequest, 16)}
}

func (pipeline *fakePipeline) Prepare(
	_ context.Context,
	batch Batch,
) (Candidate, error) {
	response := make(chan preparationResponse, 1)
	pipeline.requests <- preparationRequest{
		batch:   batch,
		respond: response,
	}
	result := <-response
	return result.candidate, result.err
}

type fakeLauncher struct {
	started    chan *fakeProcess
	startCount atomic.Int64
}

func newFakeLauncher() *fakeLauncher {
	return &fakeLauncher{started: make(chan *fakeProcess, 16)}
}

func (launcher *fakeLauncher) Start(
	_ context.Context,
	_ Candidate,
) (Process, error) {
	process := &fakeProcess{wait: make(chan error, 1)}
	launcher.startCount.Add(1)
	launcher.started <- process
	return process, nil
}

type fakeProcess struct {
	wait     chan error
	stops    atomic.Int64
	stopOnce sync.Once
	stopErr  error
}

func (process *fakeProcess) Wait() <-chan error {
	return process.wait
}

func (process *fakeProcess) Stop(ctx context.Context) error {
	process.stops.Add(1)
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	process.stopOnce.Do(func() {
		process.wait <- nil
		close(process.wait)
	})
	return process.stopErr
}

type fakeSink struct {
	events chan Event
}

func newFakeSink() *fakeSink {
	return &fakeSink{events: make(chan Event, 64)}
}

func (sink *fakeSink) Emit(event Event) {
	sink.events <- event
}

type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

func newFakeClock(now time.Time) *fakeClock {
	return &fakeClock{now: now}
}

func (clock *fakeClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

func (clock *fakeClock) NewTimer(duration time.Duration) Timer {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	timer := &fakeTimer{
		clock:    clock,
		channel:  make(chan time.Time, 1),
		deadline: clock.now.Add(duration),
		active:   true,
	}
	clock.timers = append(clock.timers, timer)
	return timer
}

func (clock *fakeClock) Advance(duration time.Duration) {
	clock.mu.Lock()
	clock.now = clock.now.Add(duration)
	now := clock.now
	timers := append([]*fakeTimer(nil), clock.timers...)
	for _, timer := range timers {
		if timer.active && !timer.deadline.After(now) {
			timer.active = false
			timer.channel <- now
		}
	}
	clock.mu.Unlock()
}

type fakeTimer struct {
	clock    *fakeClock
	channel  chan time.Time
	deadline time.Time
	active   bool
}

func (timer *fakeTimer) C() <-chan time.Time {
	return timer.channel
}

func (timer *fakeTimer) Reset(duration time.Duration) bool {
	timer.clock.mu.Lock()
	defer timer.clock.mu.Unlock()
	wasActive := timer.active
	timer.active = true
	timer.deadline = timer.clock.now.Add(duration)
	return wasActive
}

func (timer *fakeTimer) Stop() bool {
	timer.clock.mu.Lock()
	defer timer.clock.mu.Unlock()
	wasActive := timer.active
	timer.active = false
	return wasActive
}

func receivePreparation(
	t *testing.T,
	requests <-chan preparationRequest,
) preparationRequest {
	t.Helper()
	select {
	case request := <-requests:
		return request
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for preparation request")
		return preparationRequest{}
	}
}

func receiveProcess(t *testing.T, processes <-chan *fakeProcess) *fakeProcess {
	t.Helper()
	select {
	case process := <-processes:
		return process
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for process")
		return nil
	}
}

func waitForEvent(
	t *testing.T,
	events <-chan Event,
	kind EventKind,
) Event {
	t.Helper()
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case event := <-events:
			if event.Kind == kind {
				return event
			}
		case <-timeout.C:
			t.Fatalf("timed out waiting for event %s", kind)
			return Event{}
		}
	}
}

func receiveRunResult(t *testing.T, results <-chan error) error {
	t.Helper()
	select {
	case result := <-results:
		return result
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for engine result")
		return nil
	}
}

func testCandidate(
	t *testing.T,
	artifact string,
) (Candidate, *atomic.Int64) {
	t.Helper()
	disposals := &atomic.Int64{}
	candidate, err := NewCandidate(artifact, func() error {
		disposals.Add(1)
		return nil
	})
	if err != nil {
		t.Fatalf("NewCandidate() error = %v", err)
	}
	return candidate, disposals
}
