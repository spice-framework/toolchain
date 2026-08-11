//go:build !windows

package boundarygate

import "testing"

func TestNonWindowsReleaseExecutionRejectsEphemeralRunnerAcknowledgement(t *testing.T) {
	t.Parallel()
	if err := validateReleaseExecutionBoundary(""); err != nil {
		t.Fatal(err)
	}
	if err := validateReleaseExecutionBoundary("1"); err == nil {
		t.Fatal("non-Windows execution accepted a Windows ephemeral-runner acknowledgement")
	}
}
