package spicegen

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/StevenBuglione/spice/config"
	"github.com/StevenBuglione/spice/lifecycle"
)

func TestGeneratedPetclinicServesWelcomeAndManagement(t *testing.T) {
	t.Parallel()

	application, err := NewApplication(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if stopErr := application.Stop(t.Context()); stopErr != nil {
			t.Errorf("Stop() error = %v", stopErr)
		}
	})
	handler := application.Handler()
	if handler == nil {
		t.Fatal("generated application handler is nil")
	}

	welcome := httptest.NewRecorder()
	handler.ServeHTTP(
		welcome,
		httptest.NewRequest(http.MethodGet, "/", nil),
	)
	if welcome.Code != http.StatusOK ||
		!strings.Contains(welcome.Body.String(), "<h1>Welcome</h1>") {
		t.Fatalf("welcome response = %d %s", welcome.Code, welcome.Body)
	}

	health := httptest.NewRecorder()
	handler.ServeHTTP(
		health,
		httptest.NewRequest(http.MethodGet, "/actuator/health", nil),
	)
	if health.Code != http.StatusOK ||
		!strings.Contains(health.Body.String(), `"status":"UP"`) {
		t.Fatalf("health response = %d %s", health.Code, health.Body)
	}
}

func TestGeneratedPetclinicLifecycleAndTypedComponents(t *testing.T) {
	t.Parallel()

	application, err := NewApplicationWithOptions(
		t.Context(),
		ApplicationOptions{Logger: testLogger()},
	)
	if err != nil {
		t.Fatal(err)
	}
	components := application.Components()
	if components.PetclinicDatabase == nil ||
		components.OwnerRepository == nil ||
		components.PetTypeRepository == nil ||
		components.VetRepository == nil ||
		components.WelcomeController == nil ||
		components.Renderer == nil ||
		components.Mux == nil {
		t.Fatalf("components = %#v", components)
	}
	if application.State() != lifecycle.StateConstructed {
		t.Fatalf("state = %s", application.State())
	}
	if application.ShutdownTimeout() != 10*time.Second {
		t.Fatalf("shutdown timeout = %s", application.ShutdownTimeout())
	}
	if err := application.RegisterObserver(func(
		context.Context,
		lifecycle.Observation,
	) {
	}); err != nil {
		t.Fatal(err)
	}
	if err := application.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := application.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestGeneratedPetclinicNilAndConfigurationBoundaries(t *testing.T) {
	t.Parallel()

	var application *Application
	if application.State() != lifecycle.StateInvalid ||
		application.ShutdownTimeout() != 0 ||
		application.Handler() != nil ||
		application.Components().Mux != nil {
		t.Fatal("nil application accessors were not safe")
	}
	if err := application.Start(t.Context()); err == nil {
		t.Fatal("nil Start() succeeded")
	}
	if err := application.Stop(t.Context()); err == nil {
		t.Fatal("nil Stop() succeeded")
	}
	if err := application.RegisterObserver(func(
		context.Context,
		lifecycle.Observation,
	) {
	}); err == nil {
		t.Fatal("nil RegisterObserver() succeeded")
	}
	if err := application.Run(
		t.Context(),
		func() (context.Context, context.CancelFunc) {
			return context.WithCancel(context.Background())
		},
	); err == nil {
		t.Fatal("nil Run() succeeded")
	}
	if _, err := NewApplication(nil); err == nil { //nolint:staticcheck // verifies generated boundary
		t.Fatal("NewApplication(nil) succeeded")
	}
	source, err := config.NewMapSource("invalid", map[string]string{
		"spice.shutdown-timeout": "0s",
	})
	if err != nil {
		t.Fatal(err)
	}
	if constructed, constructErr := NewApplicationWithOptions(
		t.Context(),
		ApplicationOptions{
			Sources: []config.Source{source},
			Logger:  testLogger(),
		},
	); constructErr == nil {
		if constructed != nil {
			if stopErr := constructed.Stop(t.Context()); stopErr != nil {
				t.Errorf("Stop() error = %v", stopErr)
			}
		}
		t.Fatal("zero shutdown timeout succeeded")
	}
}

func TestGeneratedPetclinicCommandCheckAndFailures(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	if exit := RunCommand(CommandOptions{
		Context:   t.Context(),
		Arguments: []string{"-check"},
		Stdout:    &stdout,
		Logger:    testLogger(),
	}); exit != ExitSuccess {
		t.Fatalf("check exit = %d", exit)
	}
	if strings.TrimSpace(stdout.String()) != "Spice petclinic ready." {
		t.Fatalf("stdout = %q", stdout.String())
	}

	invalid, err := config.NewMapSource("invalid-command", map[string]string{
		"spice.shutdown-timeout": "invalid",
	})
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		want int
		opts CommandOptions
	}{
		{name: "nil context", want: ExitFailure},
		{
			name: "negative timeout",
			want: ExitFailure,
			opts: CommandOptions{
				Context:         t.Context(),
				ShutdownTimeout: -time.Second,
			},
		},
		{
			name: "unknown flag",
			want: ExitUsage,
			opts: CommandOptions{
				Context:   t.Context(),
				Arguments: []string{"-unknown"},
			},
		},
		{
			name: "positional",
			want: ExitUsage,
			opts: CommandOptions{
				Context:   t.Context(),
				Arguments: []string{"unexpected"},
			},
		},
		{
			name: "invalid configuration",
			want: ExitFailure,
			opts: CommandOptions{
				Context: t.Context(),
				Application: ApplicationOptions{
					Sources: []config.Source{invalid},
				},
			},
		},
		{
			name: "invalid shutdown factory",
			want: ExitFailure,
			opts: CommandOptions{
				Context:   t.Context(),
				Arguments: []string{"-check"},
				ShutdownContext: func(
					time.Duration,
				) (context.Context, context.CancelFunc) {
					return nil, func() {}
				},
			},
		},
		{
			name: "failed output",
			want: ExitFailure,
			opts: CommandOptions{
				Context:   t.Context(),
				Arguments: []string{"-check"},
				Stdout:    errorWriter{},
			},
		},
	}
	for _, test := range tests {
		test.opts.Logger = testLogger()
		if exit := RunCommand(test.opts); exit != test.want {
			t.Fatalf("%s exit = %d, want %d", test.name, exit, test.want)
		}
	}
}

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func testLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}
