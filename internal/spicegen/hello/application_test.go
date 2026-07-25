package spicegen

import (
	"context"
	"testing"
	"time"

	"github.com/StevenBuglione/spice/lifecycle"
)

func TestGeneratedApplicationRunsAndDrainsHTTP(t *testing.T) {
	t.Setenv("SPICE_EXAMPLE_ADDRESS", "127.0.0.1:0")
	application, err := NewApplication(context.Background())
	if err != nil {
		t.Fatalf("NewApplication() error = %v", err)
	}
	if got := application.State(); got != lifecycle.StateConstructed {
		t.Fatalf("State() = %q, want %q", got, lifecycle.StateConstructed)
	}

	runContext, cancelRun := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() {
		runResult <- application.Run(runContext, func() (context.Context, context.CancelFunc) {
			return context.WithTimeout(context.Background(), 2*time.Second)
		})
	}()

	waitUntilReady(t, application, runResult)
	cancelRun()
	if err := <-runResult; err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := application.State(); got != lifecycle.StateStopped {
		t.Fatalf("State() = %q, want %q", got, lifecycle.StateStopped)
	}
	if err := application.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop() error = %v", err)
	}
}

func TestGeneratedApplicationRejectsInvalidUse(t *testing.T) {
	//nolint:staticcheck // Explicitly verifies the generated nil-context contract.
	if application, err := NewApplication(nil); application != nil || err == nil {
		t.Fatalf("NewApplication(nil) = %#v, %v", application, err)
	}
	var application *Application
	if got := application.State(); got != lifecycle.StateInvalid {
		t.Fatalf("nil State() = %q, want %q", got, lifecycle.StateInvalid)
	}
	if err := application.Start(context.Background()); err == nil {
		t.Fatal("nil Start() error = nil")
	}
	if err := application.Stop(context.Background()); err == nil {
		t.Fatal("nil Stop() error = nil")
	}
	if err := application.Run(
		context.Background(),
		func() (context.Context, context.CancelFunc) {
			return context.WithCancel(context.Background())
		},
	); err == nil {
		t.Fatal("nil Run() error = nil")
	}
}

func waitUntilReady(t *testing.T, application *Application, runResult <-chan error) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if application.State() == lifecycle.StateReady {
			return
		}
		select {
		case err := <-runResult:
			t.Fatalf("Run() returned before ready: %v", err)
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	t.Fatalf("State() = %q, want %q", application.State(), lifecycle.StateReady)
}
