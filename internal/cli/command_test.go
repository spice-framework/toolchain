package cli

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestCommandRunsVersionThroughProductionBoundary(t *testing.T) {
	t.Parallel()

	command, err := newBootstrapCommand(NewRuntime())
	if err != nil {
		t.Fatalf("newBootstrapCommand() error = %v", err)
	}
	var stdout, stderr bytes.Buffer
	exitCode := command.Run(
		[]string{"version"},
		strings.NewReader(""),
		&stdout,
		&stderr,
	)
	if exitCode != 0 ||
		stdout.String() != "spice "+Version+" ("+Commit+")\n" ||
		stderr.Len() != 0 {
		t.Fatalf(
			"Run(version) = %d, stdout=%q, stderr=%q",
			exitCode,
			stdout.String(),
			stderr.String(),
		)
	}
}

func TestNilCommandFailsClosed(t *testing.T) {
	t.Parallel()

	var command *Command
	var stderr bytes.Buffer
	if exitCode := command.Run(
		nil,
		strings.NewReader(""),
		io.Discard,
		&stderr,
	); exitCode != 1 ||
		!strings.Contains(stderr.String(), "unavailable") {
		t.Fatalf("Run(nil) = %d, stderr=%q", exitCode, stderr.String())
	}
}

func TestCommandRejectsInvalidHandlerGraphs(t *testing.T) {
	t.Parallel()

	runtime := NewRuntime()
	help, err := NewHelpHandler(runtime)
	if err != nil {
		t.Fatalf("NewHelpHandler() error = %v", err)
	}
	duplicate, err := newCommandHandler(
		runtime,
		[]string{"help"},
		func(*Runtime, Invocation) int { return 0 },
	)
	if err != nil {
		t.Fatalf("newCommandHandler() error = %v", err)
	}
	tests := []struct {
		name     string
		handlers []Handler
		want     string
	}{
		{name: "empty", want: "no handlers"},
		{name: "nil", handlers: []Handler{nil}, want: "is nil"},
		{
			name:     "missing help",
			handlers: []Handler{mustVersionHandler(t, runtime)},
			want:     "help handler is missing",
		},
		{
			name:     "duplicate",
			handlers: []Handler{help, duplicate},
			want:     `duplicate handler name "help"`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if _, err := NewCommand(test.handlers); err == nil ||
				!strings.Contains(err.Error(), test.want) {
				t.Fatalf("NewCommand() error = %v, want %q", err, test.want)
			}
		})
	}
}

func mustVersionHandler(t *testing.T, runtime *Runtime) Handler {
	t.Helper()
	handler, err := NewVersionHandler(runtime)
	if err != nil {
		t.Fatalf("NewVersionHandler() error = %v", err)
	}
	return handler
}
