package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spice-framework/spice/compiler/load"
	"github.com/spice-framework/spice/internal/devloop"
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
	waitForBufferText(t, output, "revision 2 failed")
	waitForBufferText(t, output, "last-known-good revision 1")

	if err := os.WriteFile(mainPath, original, 0o600); err != nil {
		t.Fatalf("WriteFile(fixed main.go) error = %v", err)
	}
	waitForBufferText(t, output, "graceful restart requested for revision 3")
	waitForBufferText(t, output, "application started (revision 3)")

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
