package spicegen

import (
	"context"
	"testing"

	"github.com/StevenBuglione/spice/bean"
	"github.com/StevenBuglione/spice/internal/cli"
	"github.com/StevenBuglione/spice/lifecycle"
)

func TestApplicationConstructsTypedCommandAndLifecycle(t *testing.T) {
	t.Parallel()

	application, err := NewApplication(context.Background())
	if err != nil {
		t.Fatalf("NewApplication() error = %v", err)
	}
	if application.Components().Command == nil {
		t.Fatal("Components().Command = nil")
	}
	if application.State() != lifecycle.StateConstructed {
		t.Fatalf("State() = %q, want %q", application.State(), lifecycle.StateConstructed)
	}
	if err := application.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if application.State() != lifecycle.StateReady {
		t.Fatalf("State() after Start = %q, want %q", application.State(), lifecycle.StateReady)
	}
	if err := application.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if err := application.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop() error = %v", err)
	}
	if application.State() != lifecycle.StateStopped {
		t.Fatalf("State() after Stop = %q, want %q", application.State(), lifecycle.StateStopped)
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
						return cli.NewCommand(), func(context.Context) error {
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

func TestApplicationRejectsNilConstructionContext(t *testing.T) {
	t.Parallel()

	//nolint:staticcheck // The generated boundary deliberately rejects nil.
	if _, err := NewApplication(nil); err == nil {
		t.Fatal("NewApplication(nil) error = nil")
	}
}
