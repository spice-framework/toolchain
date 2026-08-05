package spice_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/StevenBuglione/spice/bean"
	"github.com/StevenBuglione/spice/config"
	"github.com/StevenBuglione/spice/internal/cli"
	spicegen "github.com/StevenBuglione/spice/internal/spicegen/spice"
	"github.com/StevenBuglione/spice/lifecycle"
	"github.com/StevenBuglione/spice/spicetest"
)

type (
	Application        = spicegen.Application
	ApplicationOptions = spicegen.ApplicationOptions
	BeanOverrides      = spicegen.BeanOverrides
)

var (
	NewApplication            = spicegen.NewApplication
	NewApplicationWithOptions = spicegen.NewApplicationWithOptions
)

func TestApplicationConstructsTypedCommandAndLifecycle(t *testing.T) {
	t.Parallel()

	testContext, err := spicetest.NewContext(
		context.Background(),
		func(ctx context.Context) (*Application, error) {
			return NewApplication(ctx)
		},
		spicetest.ContextOptions{
			StartupTimeout:  time.Second,
			ShutdownTimeout: time.Second,
		},
	)
	if err != nil {
		t.Fatalf("spicetest.NewContext() error = %v", err)
	}
	application := testContext.Application()
	if application.Components().Command == nil {
		t.Fatal("Components().Command = nil")
	}
	components := application.Components()
	if components.Runtime == nil {
		t.Fatal("Components().Runtime = nil")
	}
	handlers := []cli.Handler{
		components.HelpHandler,
		components.VersionHandler,
		components.VerifyHandler,
		components.AnnotationsHandler,
		components.ModulesHandler,
		components.BeansHandler,
		components.GeneratedHandler,
		components.TestHandler,
		components.GenerateHandler,
		components.BuildHandler,
		components.RunHandler,
		components.DevHandler,
		components.LspHandler,
	}
	if slices.Contains(handlers, nil) {
		t.Fatalf("Components() contains a nil handler: %#v", handlers)
	}
	if testContext.State() != lifecycle.StateReady {
		t.Fatalf("State() = %q, want %q", testContext.State(), lifecycle.StateReady)
	}
	if err := testContext.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := testContext.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if application.State() != lifecycle.StateStopped {
		t.Fatalf("State() after Close = %q, want %q", application.State(), lifecycle.StateStopped)
	}
}

func TestApplicationUsesGeneratedConfigurationSchema(t *testing.T) {
	t.Parallel()

	source, err := config.NewMapSource("test", map[string]string{
		"spice.shutdown-timeout": "275ms",
	})
	if err != nil {
		t.Fatalf("config.NewMapSource() error = %v", err)
	}
	application, err := NewApplicationWithOptions(
		context.Background(),
		ApplicationOptions{Sources: []config.Source{source}},
	)
	if err != nil {
		t.Fatalf("NewApplicationWithOptions() error = %v", err)
	}
	t.Cleanup(func() {
		if stopErr := application.Stop(context.Background()); stopErr != nil {
			t.Errorf("Stop() error = %v", stopErr)
		}
	})
	if application.ShutdownTimeout() != 275*time.Millisecond {
		t.Fatalf(
			"ShutdownTimeout() = %s, want 275ms",
			application.ShutdownTimeout(),
		)
	}

	unknown, err := config.NewMapSource("unknown", map[string]string{
		"spice.undeclared": "true",
	})
	if err != nil {
		t.Fatalf("config.NewMapSource(unknown) error = %v", err)
	}
	if _, err := NewApplicationWithOptions(
		context.Background(),
		ApplicationOptions{Sources: []config.Source{unknown}},
	); err == nil || !strings.Contains(err.Error(), `unknown property "spice.undeclared"`) {
		t.Fatalf("unknown configuration error = %v", err)
	}
}

func TestApplicationInjectsTypedInterfaceOverrideIntoCommandCollection(t *testing.T) {
	t.Parallel()

	override := versionOverrideHandler{}
	application, err := NewApplicationWithOptions(
		context.Background(),
		ApplicationOptions{
			Overrides: BeanOverrides{
				VersionHandler: bean.Replace[cli.Handler](override),
			},
		},
	)
	if err != nil {
		t.Fatalf("NewApplicationWithOptions() error = %v", err)
	}
	t.Cleanup(func() {
		if stopErr := application.Stop(context.Background()); stopErr != nil {
			t.Errorf("Stop() error = %v", stopErr)
		}
	})

	if application.Components().VersionHandler != override {
		t.Fatalf(
			"Components().VersionHandler = %#v, want typed override",
			application.Components().VersionHandler,
		)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exitCode := application.Components().Command.Run(
		[]string{"version"},
		strings.NewReader(""),
		&stdout,
		&stderr,
	); exitCode != 0 ||
		stdout.String() != "dogfooded version\n" ||
		stderr.Len() != 0 {
		t.Fatalf(
			"Command.Run(version) = %d, stdout=%q, stderr=%q",
			exitCode,
			stdout.String(),
			stderr.String(),
		)
	}
}

func TestApplicationRoutesLSPThroughGeneratedCommandGraph(t *testing.T) {
	t.Parallel()

	application, err := NewApplication(context.Background())
	if err != nil {
		t.Fatalf("NewApplication() error = %v", err)
	}
	t.Cleanup(func() {
		if stopErr := application.Stop(context.Background()); stopErr != nil {
			t.Errorf("Stop() error = %v", stopErr)
		}
	})

	var input bytes.Buffer
	writeLSPMessage(t, &input, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	writeLSPMessage(t, &input, `{"jsonrpc":"2.0","id":2,"method":"shutdown"}`)
	writeLSPMessage(t, &input, `{"jsonrpc":"2.0","method":"exit"}`)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if exitCode := application.Components().Command.Run(
		[]string{"lsp"},
		&input,
		&stdout,
		&stderr,
	); exitCode != 0 ||
		bytes.Count(stdout.Bytes(), []byte("Content-Length:")) != 2 ||
		!bytes.Contains(stdout.Bytes(), []byte(`"completionProvider"`)) ||
		stderr.Len() != 0 {
		t.Fatalf(
			"Command.Run(lsp) = %d, stdout=%q, stderr=%q",
			exitCode,
			stdout.String(),
			stderr.String(),
		)
	}
}

func TestApplicationUsesTypedCommandOverrideAndOwnsCleanup(t *testing.T) {
	t.Parallel()

	factoryCalls := 0
	cleanupCalls := 0
	application, err := NewApplicationWithOptions(
		context.Background(),
		ApplicationOptions{
			Overrides: BeanOverrides{
				Command: bean.ReplaceFactory(
					func(context.Context) (*cli.Command, lifecycle.Cleanup, error) {
						factoryCalls++
						help, err := cli.NewHelpHandler(cli.NewRuntime())
						if err != nil {
							return nil, nil, err
						}
						command, err := cli.NewCommand([]cli.Handler{help})
						if err != nil {
							return nil, nil, err
						}
						return command, func(context.Context) error {
							cleanupCalls++
							return nil
						}, nil
					},
				),
			},
		},
	)
	if err != nil {
		t.Fatalf("NewApplicationWithOptions() error = %v", err)
	}
	if factoryCalls != 1 || application.Components().Command == nil {
		t.Fatalf(
			"override construction calls = %d, command nil = %t",
			factoryCalls,
			application.Components().Command == nil,
		)
	}
	if err := application.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if err := application.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop() error = %v", err)
	}
	if cleanupCalls != 1 {
		t.Fatalf("override cleanup calls = %d, want 1", cleanupCalls)
	}
}

type versionOverrideHandler struct{}

func (versionOverrideHandler) Names() []string {
	return []string{"version", "--version"}
}

func (versionOverrideHandler) Run(invocation cli.Invocation) int {
	if _, err := io.WriteString(invocation.Stdout, "dogfooded version\n"); err != nil {
		return 1
	}
	return 0
}

func writeLSPMessage(t *testing.T, output *bytes.Buffer, content string) {
	t.Helper()
	if _, err := fmt.Fprintf(
		output,
		"Content-Length: %d\r\n\r\n%s",
		len(content),
		content,
	); err != nil {
		t.Fatalf("write LSP message: %v", err)
	}
}

func TestApplicationRejectsNilConstructionContext(t *testing.T) {
	t.Parallel()

	if _, err := NewApplication(nil); err == nil {
		t.Fatal("NewApplication(nil) error = nil")
	}
}
