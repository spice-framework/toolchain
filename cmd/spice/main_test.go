package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/spice-framework/spice/internal/cli"
	spiceapp "github.com/spice-framework/spice/internal/spicegen/spice"
)

func TestRunUsesGeneratedProductionApplication(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	if exitCode := run([]string{"version"}, &stdout, &stderr); exitCode != 0 ||
		stdout.String() != "spice "+cli.Version+"\n" ||
		stderr.Len() != 0 {
		t.Fatalf(
			"run(version) = %d, stdout=%q, stderr=%q",
			exitCode,
			stdout.String(),
			stderr.String(),
		)
	}
}

func TestRunPreservesCommandUsageFailure(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	if exitCode := run([]string{"not-a-command"}, &stdout, &stderr); exitCode != 2 ||
		!strings.Contains(stderr.String(), `unknown command "not-a-command"`) {
		t.Fatalf(
			"run(unknown) = %d, stdout=%q, stderr=%q",
			exitCode,
			stdout.String(),
			stderr.String(),
		)
	}
}

func TestRunRejectsInvalidApplicationFactory(t *testing.T) {
	t.Parallel()

	want := errors.New("construction failed")
	tests := []struct {
		name    string
		factory applicationFactory
		want    string
	}{
		{name: "nil factory", want: "construct Spice application failed"},
		{
			name: "factory error",
			factory: func(context.Context) (runnableApplication, error) {
				return nil, want
			},
			want: want.Error(),
		},
		{
			name: "nil application",
			factory: func(context.Context) (runnableApplication, error) {
				//nolint:nilnil // Exercises the production entrypoint's fail-closed boundary.
				return nil, nil
			},
			want: "construct Spice application failed",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var stderr bytes.Buffer
			if exitCode := runWithApplication(
				nil,
				nil,
				&stderr,
				test.factory,
			); exitCode != 1 || !strings.Contains(stderr.String(), test.want) {
				t.Fatalf(
					"runWithApplication() = %d, stderr=%q",
					exitCode,
					stderr.String(),
				)
			}
		})
	}
}

func TestRunReportsLifecycleFailures(t *testing.T) {
	t.Parallel()

	startFailure := errors.New("start failed")
	stopFailure := errors.New("stop failed")
	tests := []struct {
		name        string
		arguments   []string
		application *fakeApplication
		wantCode    int
		wantError   string
	}{
		{
			name: "start",
			application: &fakeApplication{
				startErr: startFailure,
			},
			wantCode:  1,
			wantError: startFailure.Error(),
		},
		{
			name:      "stop after success",
			arguments: []string{"version"},
			application: &fakeApplication{
				command: testCommand(t),
				stopErr: stopFailure,
			},
			wantCode:  1,
			wantError: stopFailure.Error(),
		},
		{
			name:      "stop preserves usage failure",
			arguments: []string{"unknown"},
			application: &fakeApplication{
				command:         testCommand(t),
				stopErr:         stopFailure,
				shutdownTimeout: -time.Second,
			},
			wantCode:  2,
			wantError: stopFailure.Error(),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer
			exitCode := runWithApplication(
				test.arguments,
				&stdout,
				&stderr,
				func(context.Context) (runnableApplication, error) {
					return test.application, nil
				},
			)
			if exitCode != test.wantCode ||
				!strings.Contains(stderr.String(), test.wantError) {
				t.Fatalf(
					"runWithApplication() = %d, stderr=%q",
					exitCode,
					stderr.String(),
				)
			}
		})
	}
}

func testCommand(t *testing.T) *cli.Command {
	t.Helper()
	runtime := cli.NewRuntime()
	help, err := cli.NewHelpHandler(runtime)
	if err != nil {
		t.Fatalf("NewHelpHandler() error = %v", err)
	}
	version, err := cli.NewVersionHandler(runtime)
	if err != nil {
		t.Fatalf("NewVersionHandler() error = %v", err)
	}
	command, err := cli.NewCommand([]cli.Handler{help, version})
	if err != nil {
		t.Fatalf("NewCommand() error = %v", err)
	}
	return command
}

func TestWriteFailureHandlesAbsentAndFailingWriters(t *testing.T) {
	t.Parallel()

	if err := writeFailure(nil, "operation", nil); err != nil {
		t.Fatalf("writeFailure(nil) error = %v", err)
	}
	want := errors.New("write failed")
	if err := writeFailure(
		errorWriter{err: want},
		"operation",
		want,
	); !errors.Is(err, want) {
		t.Fatalf("writeFailure(failing) error = %v, want %v", err, want)
	}
}

type fakeApplication struct {
	command         *cli.Command
	startErr        error
	stopErr         error
	shutdownTimeout time.Duration
}

func (application *fakeApplication) Start(context.Context) error {
	return application.startErr
}

func (application *fakeApplication) Stop(context.Context) error {
	return application.stopErr
}

func (application *fakeApplication) ShutdownTimeout() time.Duration {
	return application.shutdownTimeout
}

func (application *fakeApplication) Components() spiceapp.Components {
	return spiceapp.Components{Command: application.command}
}

type errorWriter struct {
	err error
}

func (writer errorWriter) Write([]byte) (int, error) {
	return 0, writer.err
}
