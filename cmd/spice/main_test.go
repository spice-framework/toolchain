package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spice-framework/toolchain/internal/cli"
)

func TestRunDelegatesToHandwrittenCLI(t *testing.T) {
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
