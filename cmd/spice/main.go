package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	spiceapp "github.com/spice-framework/spice/internal/spicegen/spice"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

type runnableApplication interface {
	Start(context.Context) error
	Stop(context.Context) error
	ShutdownTimeout() time.Duration
	Components() spiceapp.Components
}

type applicationFactory func(context.Context) (runnableApplication, error)

func run(arguments []string, stdout, stderr io.Writer) int {
	return runWithApplication(
		arguments,
		stdout,
		stderr,
		newApplication,
	)
}

func newApplication(ctx context.Context) (runnableApplication, error) {
	return spiceapp.NewApplication(ctx)
}

func runWithApplication(
	arguments []string,
	stdout io.Writer,
	stderr io.Writer,
	factory applicationFactory,
) int {
	if factory == nil {
		if writeErr := writeFailure(
			stderr,
			"construct Spice application",
			nil,
		); writeErr != nil {
			return 1
		}
		return 1
	}
	ctx := context.Background()
	application, err := factory(ctx)
	if err != nil {
		if writeErr := writeFailure(
			stderr,
			"construct Spice application",
			err,
		); writeErr != nil {
			return 1
		}
		return 1
	}
	if application == nil {
		if writeErr := writeFailure(
			stderr,
			"construct Spice application",
			nil,
		); writeErr != nil {
			return 1
		}
		return 1
	}
	if err := application.Start(ctx); err != nil {
		if writeErr := writeFailure(
			stderr,
			"start Spice application",
			err,
		); writeErr != nil {
			return 1
		}
		return 1
	}

	exitCode := application.Components().Command.Run(
		arguments,
		os.Stdin,
		stdout,
		stderr,
	)
	timeout := application.ShutdownTimeout()
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	shutdown, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := application.Stop(shutdown); err != nil {
		if writeErr := writeFailure(
			stderr,
			"stop Spice application",
			err,
		); writeErr != nil {
			return 1
		}
		if exitCode == 0 {
			return 1
		}
	}
	return exitCode
}

func writeFailure(stderr io.Writer, operation string, err error) error {
	if stderr == nil {
		return nil
	}
	if err == nil {
		_, writeErr := fmt.Fprintf(
			stderr,
			"%s failed.\n",
			operation,
		)
		return writeErr
	}
	_, writeErr := fmt.Fprintf(
		stderr,
		"%s failed: %v\n",
		operation,
		err,
	)
	return writeErr
}
