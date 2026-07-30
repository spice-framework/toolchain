package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestCommandHandlerCopiesMetadataAndInvocation(t *testing.T) {
	t.Parallel()

	names := []string{"zeta", "alpha"}
	arguments := []string{"original"}
	handler, err := newCommandHandler(
		NewRuntime(),
		names,
		func(_ *Runtime, invocation Invocation) int {
			invocation.Arguments[0] = "changed"
			return 7
		},
	)
	if err != nil {
		t.Fatalf("newCommandHandler() error = %v", err)
	}
	names[0] = "mutated"
	gotNames := handler.Names()
	if len(gotNames) != 2 ||
		strings.Join(gotNames, ",") != "alpha,zeta" {
		t.Fatalf("Names() = %#v", gotNames)
	}
	gotNames[0] = "mutated"
	namesAfterMutation := handler.Names()
	if len(namesAfterMutation) == 0 || namesAfterMutation[0] != "alpha" {
		t.Fatalf("Names() retained caller mutation: %#v", namesAfterMutation)
	}
	if exitCode := handler.Run(Invocation{
		Arguments: arguments,
	}); exitCode != 7 {
		t.Fatalf("Run() = %d, want 7", exitCode)
	}
	if arguments[0] != "original" {
		t.Fatalf("Run() mutated caller arguments: %#v", arguments)
	}
}

func TestCommandHandlerRejectsInvalidConstruction(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		runtime *Runtime
		names   []string
		run     func(*Runtime, Invocation) int
		want    string
	}{
		{
			name:  "nil runtime",
			names: []string{"valid"},
			run:   func(*Runtime, Invocation) int { return 0 },
			want:  "runtime is nil",
		},
		{
			name:    "nil function",
			runtime: NewRuntime(),
			names:   []string{"valid"},
			want:    "function is nil",
		},
		{
			name:    "blank name",
			runtime: NewRuntime(),
			names:   []string{""},
			run:     func(*Runtime, Invocation) int { return 0 },
			want:    "name 0 is invalid",
		},
		{
			name:    "duplicate name",
			runtime: NewRuntime(),
			names:   []string{"same", "same"},
			run:     func(*Runtime, Invocation) int { return 0 },
			want:    `repeats name "same"`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if _, err := newCommandHandler(
				test.runtime,
				test.names,
				test.run,
			); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf(
					"newCommandHandler() error = %v, want %q",
					err,
					test.want,
				)
			}
		})
	}
}

func TestNilCommandHandlerFailsClosed(t *testing.T) {
	t.Parallel()

	var handler *commandHandler
	var stderr bytes.Buffer
	if exitCode := handler.Run(Invocation{Stderr: &stderr}); exitCode != 1 ||
		!strings.Contains(stderr.String(), "unavailable") {
		t.Fatalf("Run() = %d, stderr=%q", exitCode, stderr.String())
	}
	if handler.Names() != nil {
		t.Fatalf("Names() = %#v, want nil", handler.Names())
	}
}
