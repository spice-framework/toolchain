package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spice-framework/toolchain/compiler/load"
	"github.com/spice-framework/toolchain/internal/devloop"
)

func TestParseDevArguments(t *testing.T) {
	t.Parallel()
	parsed, err := parseDevArguments([]string{
		"--target=Shop",
		"--quiet", "25ms",
		"--max-delay=200ms",
		"--poll", "50ms",
		"--stop-timeout=3s",
		"--include", "assets/**",
		"--include=schema/*.graphql",
		"--exclude", "assets/private/**",
		"./cmd/shop",
		"--",
		"-config", "development",
	})
	if err != nil {
		t.Fatalf("parseDevArguments() error = %v", err)
	}
	if parsed.generation.target != "Shop" ||
		len(parsed.generation.patterns) != 1 ||
		parsed.generation.patterns[0] != "./cmd/shop" ||
		parsed.quiet != 25*time.Millisecond ||
		parsed.maxDelay != 200*time.Millisecond ||
		parsed.poll != 50*time.Millisecond ||
		parsed.stopTimeout != 3*time.Second {
		t.Fatalf("parseDevArguments() = %+v", parsed)
	}
	if strings.Join(parsed.includes, ",") !=
		"assets/**,schema/*.graphql" {
		t.Fatalf("includes = %v", parsed.includes)
	}
	if strings.Join(parsed.excludes, ",") != "assets/private/**" {
		t.Fatalf("excludes = %v", parsed.excludes)
	}
	if strings.Join(parsed.application, ",") != "-config,development" {
		t.Fatalf("application arguments = %v", parsed.application)
	}
}

func TestParseDevArgumentsRejectsInvalidOptions(t *testing.T) {
	t.Parallel()
	tests := [][]string{
		{"--quiet=0s"},
		{"--poll=nope"},
		{"--stop-timeout"},
		{"--quiet=1s", "--quiet=2s"},
		{"--include"},
		{"--unknown"},
		{"--check"},
	}
	for _, arguments := range tests {
		if _, err := parseDevArguments(arguments); err == nil {
			t.Errorf("parseDevArguments(%v) error = nil, want failure", arguments)
		}
	}
}

func TestDevelopmentEventWriterProducesConciseSafeEvents(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	sink := &developmentEventWriter{writer: &output}
	events := []devloop.Event{
		{
			Kind:  devloop.EventChangeDetected,
			Paths: []string{"z.go", "a.go"},
		},
		{Kind: devloop.EventAnalysisStarted, Revision: 2},
		{Kind: devloop.EventGenerationComplete, Revision: 2},
		{Kind: devloop.EventBuildComplete, Revision: 2},
		{Kind: devloop.EventCandidateReady, Revision: 2},
		{
			Kind:     devloop.EventPreparationFailed,
			Revision: 2,
			Err:      context.Canceled,
		},
		{Kind: devloop.EventLastKnownGood, Revision: 1},
		{Kind: devloop.EventCandidateDiscarded, Revision: 2},
		{Kind: devloop.EventRestartRequested, Revision: 3},
		{Kind: devloop.EventApplicationStarted, Revision: 3},
		{Kind: devloop.EventApplicationExited, Revision: 3},
		{
			Kind: devloop.EventWatchError,
			Err:  context.DeadlineExceeded,
		},
		{
			Kind: devloop.EventCleanupFailed,
			Err:  os.ErrPermission,
		},
		{
			Kind:     devloop.EventShutdownTimedOut,
			Revision: 3,
			Err:      devloop.ErrGracefulStopTimeout,
		},
	}
	for _, event := range events {
		sink.Emit(event)
	}
	if err := sink.Err(); err != nil {
		t.Fatalf("Err() = %v", err)
	}
	text := output.String()
	for _, expected := range []string{
		"change detected: a.go, z.go",
		"analysis started (revision 2)",
		"generation complete (revision 2)",
		"build complete (revision 2)",
		"revision 2 failed: context canceled",
		"last-known-good revision 1",
		"graceful restart requested for revision 3",
		"application started (revision 3)",
		"application exited (revision 3)",
		"watcher recovered from error",
		"candidate cleanup failed",
		"graceful shutdown timed out for revision 3",
	} {
		if !strings.Contains(text, expected) {
			t.Errorf("event output missing %q:\n%s", expected, text)
		}
	}
}

func TestBoundedOutputTruncatesWithoutShortWrites(t *testing.T) {
	t.Parallel()
	output := newBoundedOutput(5)
	content := []byte("123456789")
	written, err := output.Write(content)
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if written != len(content) {
		t.Fatalf("Write() = %d, want %d", written, len(content))
	}
	if got := output.String(); got != "12345\n... output truncated" {
		t.Fatalf("String() = %q", got)
	}
	output.Reset()
	if output.String() != "" {
		t.Fatalf("String() after Reset = %q, want empty", output.String())
	}
}

func TestDevelopmentWatcherContextIsEngineOwned(t *testing.T) {
	t.Parallel()
	type contextKey struct{}
	key := contextKey{}
	parent, cancel := context.WithCancel(context.WithValue(
		context.Background(),
		key,
		"workspace",
	))
	watcherContext := developmentWatcherContext(parent)
	cancel()
	if watcherContext.Err() != nil || watcherContext.Done() != nil {
		t.Fatalf("watcher context canceled with parent: %v", watcherContext.Err())
	}
	if got := watcherContext.Value(key); got != "workspace" {
		t.Fatalf("watcher context value = %v, want workspace", got)
	}
}

func TestDevCommandKeepsLastKnownGoodAndRecovers(t *testing.T) {
	root := packageMainRunModule(t)
	mainPath := filepath.Join(root, "main.go")
	original, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatalf("ReadFile(main.go) error = %v", err)
	}
	output := newNotifyingBuffer()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() {
		done <- devCommandContext(
			ctx,
			[]string{
				"--poll=20ms",
				"--quiet=20ms",
				"--max-delay=100ms",
				"--stop-timeout=5s",
			},
			strings.NewReader(""),
			output,
			output,
			load.Options{
				Dir: root,
				Env: append(
					os.Environ(),
					"GOWORK=off",
				),
			},
			load.Load,
			executeApplicationBuild,
		)
	}()
	waitForBufferText(t, output, "application started (revision 1)")

	broken := strings.Replace(
		string(original),
		"// @Application",
		"// @Application\n// @Unknown",
		1,
	)
	if err := os.WriteFile(mainPath, []byte(broken), 0o600); err != nil {
		t.Fatalf("WriteFile(broken main.go) error = %v", err)
	}
	// Watchers may coalesce or duplicate filesystem notifications, so the
	// invalid source is not guaranteed to own revision 2. The stable compiler
	// diagnostic proves the failed revision without coupling this executable
	// test to a particular watcher event count.
	waitForBufferText(t, output, "[spice.resolution.annotation-import]")
	waitForBufferText(t, output, "last-known-good revision 1")

	if err := os.WriteFile(mainPath, original, 0o600); err != nil {
		t.Fatalf("WriteFile(fixed main.go) error = %v", err)
	}
	revision := waitForBufferRevision(
		t,
		output,
		"application started (revision ",
		3,
	)
	waitForBufferText(
		t,
		output,
		"graceful restart requested for revision "+strconv.Itoa(revision),
	)
	// EventApplicationStarted proves that the operating-system process was
	// created; the generated command may not yet have installed its signal
	// handler. Wait for both successful generated applications to report
	// runtime startup before exercising graceful cancellation. This matters on
	// slower Windows hosts, where CTRL_BREAK can otherwise race process setup.
	waitForBufferTextOccurrences(t, output, `"msg":"Spice application starting"`, 2)

	cancel()
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("devCommandContext() code = %d\n%s", code, output.String())
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("timed out waiting for dev command shutdown\n%s", output.String())
	}
}

type notifyingBuffer struct {
	mu      sync.Mutex
	buffer  bytes.Buffer
	updated chan struct{}
}

func newNotifyingBuffer() *notifyingBuffer {
	return &notifyingBuffer{updated: make(chan struct{}, 1)}
}

func (buffer *notifyingBuffer) Write(content []byte) (int, error) {
	buffer.mu.Lock()
	written, err := buffer.buffer.Write(content)
	buffer.mu.Unlock()
	select {
	case buffer.updated <- struct{}{}:
	default:
	}
	return written, err
}

func (buffer *notifyingBuffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}

func waitForBufferText(
	t *testing.T,
	buffer *notifyingBuffer,
	expected string,
) {
	t.Helper()
	// This is an executable integration test: each successful revision invokes
	// the real Go compiler with the race detector active under make verify.
	// Whole-repository and installed-IDE verification can legitimately keep
	// that compiler busy beyond 30 seconds on supported CI hosts.
	timeout := time.NewTimer(90 * time.Second)
	defer timeout.Stop()
	for {
		if strings.Contains(buffer.String(), expected) {
			return
		}
		select {
		case <-buffer.updated:
		case <-timeout.C:
			t.Fatalf(
				"timed out waiting for %q\n%s",
				expected,
				buffer.String(),
			)
		}
	}
}

func waitForBufferTextOccurrences(
	t *testing.T,
	buffer *notifyingBuffer,
	expected string,
	minimum int,
) {
	t.Helper()
	timeout := time.NewTimer(90 * time.Second)
	defer timeout.Stop()
	for {
		if strings.Count(buffer.String(), expected) >= minimum {
			return
		}
		select {
		case <-buffer.updated:
		case <-timeout.C:
			t.Fatalf(
				"timed out waiting for %d occurrence(s) of %q\n%s",
				minimum,
				expected,
				buffer.String(),
			)
		}
	}
}

func waitForBufferRevision(
	t *testing.T,
	buffer *notifyingBuffer,
	prefix string,
	minimum int,
) int {
	t.Helper()
	timeout := time.NewTimer(90 * time.Second)
	defer timeout.Stop()
	for {
		if revision, found := findRevisionAtOrAfter(
			buffer.String(),
			prefix,
			minimum,
		); found {
			return revision
		}
		select {
		case <-buffer.updated:
		case <-timeout.C:
			t.Fatalf(
				"timed out waiting for %q at revision %d or later\n%s",
				prefix,
				minimum,
				buffer.String(),
			)
		}
	}
}

func findRevisionAtOrAfter(
	output string,
	prefix string,
	minimum int,
) (int, bool) {
	for line := range strings.SplitSeq(output, "\n") {
		_, suffix, found := strings.Cut(line, prefix)
		if !found {
			continue
		}
		length := 0
		for length < len(suffix) && suffix[length] >= '0' && suffix[length] <= '9' {
			length++
		}
		if length == 0 {
			continue
		}
		revision, err := strconv.Atoi(suffix[:length])
		if err == nil && revision >= minimum {
			return revision, true
		}
	}
	return 0, false
}

func TestFindRevisionAtOrAfter(t *testing.T) {
	t.Parallel()
	output := strings.Join([]string{
		"spice dev: application started (revision 1)",
		"spice dev: discarded obsolete revision 3",
		"spice dev: application started (revision 4)",
	}, "\n")

	if revision, found := findRevisionAtOrAfter(
		output,
		"application started (revision ",
		3,
	); !found || revision != 4 {
		t.Fatalf("findRevisionAtOrAfter() = %d, %t, want 4, true", revision, found)
	}
	if revision, found := findRevisionAtOrAfter(
		output,
		"application started (revision ",
		5,
	); found || revision != 0 {
		t.Fatalf("findRevisionAtOrAfter() = %d, %t, want 0, false", revision, found)
	}
}
