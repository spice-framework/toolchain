package cli

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestCommandRunsVersionThroughProductionBoundary(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	exitCode := NewCommand().Run(
		[]string{"version"},
		&stdout,
		&stderr,
	)
	if exitCode != 0 ||
		stdout.String() != "spice "+Version+"\n" ||
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
	if exitCode := command.Run(nil, io.Discard, &stderr); exitCode != 1 ||
		!strings.Contains(stderr.String(), "unavailable") {
		t.Fatalf("Run(nil) = %d, stderr=%q", exitCode, stderr.String())
	}
}
