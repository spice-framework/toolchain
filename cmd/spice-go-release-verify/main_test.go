package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestRunRejectsInvalidArguments(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		arguments []string
		wantCode  int
		wantError string
	}{
		{name: "unknown flag", arguments: []string{"-unknown"}, wantCode: 2, wantError: "flag provided"},
		{name: "positional", arguments: []string{"extra"}, wantCode: 2, wantError: "unexpected arguments"},
		{name: "missing required", wantCode: 2, wantError: "are required"},
		{
			name: "unsupported profile",
			arguments: []string{
				"-artifacts", "missing", "-verified-output", "verified", "-root", "missing", "-repository", "spice-agent",
				"-source", "https://github.com/spice-framework/spice-agent",
				"-module", "github.com/spice-framework/spice-agent", "-version", "v0.1.0-preview.1",
				"-commit", strings.Repeat("a", 40), "-profile", "go-distribution-v1",
			},
			wantCode: 1, wantError: "unsupported",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), test.arguments, &stdout, &stderr)
			if code != test.wantCode || !strings.Contains(stderr.String(), test.wantError) {
				t.Fatalf("run() = %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestWriteExitHandlesWriterFailure(t *testing.T) {
	t.Parallel()
	if code := writeExit(failingWriter{}, 2, "failure"); code != 1 {
		t.Fatalf("writeExit() = %d", code)
	}
}

func TestRunPolicyCheckAuthorizesExactModulePolicy(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{
		"policy-check",
		"--repository=spice-agent",
		"--source=https://github.com/spice-framework/spice-agent",
		"--module=github.com/spice-framework/spice-agent",
		"--version=v0.1.0-preview.4",
		"--profile=go-module-v1",
	}, &stdout, &stderr)
	want := "{\"profile\":\"go-module-v1\",\"repository\":\"spice-agent\"," +
		"\"module\":\"github.com/spice-framework/spice-agent\"," +
		"\"version\":\"v0.1.0-preview.4\"," +
		"\"source\":\"https://github.com/spice-framework/spice-agent\"}\n"
	if code != 0 || stdout.String() != want || stderr.Len() != 0 {
		t.Fatalf("run(policy-check) = %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
}

func TestRunPolicyCheckAuthorizesExactDistributionPolicy(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{
		"policy-check",
		"--repository=spice-agent-coding",
		"--source=https://github.com/spice-framework/spice-agent-coding",
		"--module=github.com/spice-framework/spice-agent-coding",
		"--version=v0.1.0-preview.2",
		"--profile=go-distribution-v1",
	}, &stdout, &stderr)
	want := "{\"profile\":\"go-distribution-v1\",\"repository\":\"spice-agent-coding\"," +
		"\"module\":\"github.com/spice-framework/spice-agent-coding\"," +
		"\"version\":\"v0.1.0-preview.2\"," +
		"\"source\":\"https://github.com/spice-framework/spice-agent-coding\"}\n"
	if code != 0 || stdout.String() != want || stderr.Len() != 0 {
		t.Fatalf("run(distribution policy-check) = %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
}

func TestRunPolicyCheckRejectsStaleDistributionPreviewOne(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{
		"policy-check",
		"--repository=spice-agent-coding",
		"--source=https://github.com/spice-framework/spice-agent-coding",
		"--module=github.com/spice-framework/spice-agent-coding",
		"--version=v0.1.0-preview.1",
		"--profile=go-distribution-v1",
	}, &stdout, &stderr)
	if code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "do not match") {
		t.Fatalf("run(stale distribution policy-check) = %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
}

func TestRunPolicyCheckFailsClosedWithBoundedOutput(t *testing.T) {
	t.Parallel()
	base := []string{
		"policy-check",
		"-repository", "spice-agent",
		"-source", "https://github.com/spice-framework/spice-agent",
		"-module", "github.com/spice-framework/spice-agent",
		"-version", "v0.1.0-preview.4",
		"-profile", "go-module-v1",
	}
	for _, test := range []struct {
		name      string
		arguments []string
		wantCode  int
		want      string
	}{
		{name: "unknown flag", arguments: []string{"policy-check", "-" + strings.Repeat("x", 2048)}, wantCode: 2, want: "invalid policy-check arguments"},
		{name: "positional", arguments: append(append([]string{}, base...), "extra"), wantCode: 2, want: "invalid policy-check arguments"},
		{name: "missing", arguments: []string{"policy-check"}, wantCode: 2, want: "policy-check requires"},
		{name: "stale preview.2", arguments: replaceArgument(base, "-version", "v0.1.0-preview.2"), wantCode: 1, want: "do not match"},
		{name: "stale preview.3", arguments: replaceArgument(base, "-version", "v0.1.0-preview.3"), wantCode: 1, want: "do not match"},
		{name: "bounded unknown", arguments: replaceArgument(base, "-repository", strings.Repeat("x", 512)), wantCode: 1, want: "not independently authorized"},
		{name: "oversized", arguments: replaceArgument(base, "-repository", strings.Repeat("x", 2048)), wantCode: 1, want: "missing or invalid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), test.arguments, &stdout, &stderr)
			if code != test.wantCode || stdout.Len() != 0 || !strings.Contains(stderr.String(), test.want) ||
				stderr.Len() > maxPolicyCheckOutputBytes {
				t.Fatalf("run(policy-check) = %d, stdout %q, stderr bytes %d: %q", code, stdout.String(), stderr.Len(), stderr.String())
			}
		})
	}
}

func TestRunPolicyCheckHandlesWriterFailure(t *testing.T) {
	t.Parallel()
	code := run(context.Background(), []string{
		"policy-check",
		"-repository", "spice-agent-coding",
		"-source", "https://github.com/spice-framework/spice-agent-coding",
		"-module", "github.com/spice-framework/spice-agent-coding",
		"-version", "v0.1.0-preview.2",
		"-profile", "go-distribution-v1",
	}, failingWriter{}, io.Discard)
	if code != 1 {
		t.Fatalf("run(policy-check writer failure) = %d", code)
	}
}

func replaceArgument(arguments []string, flagName, value string) []string {
	result := append([]string{}, arguments...)
	for index := range len(result) - 1 {
		if result[index] == flagName {
			result[index+1] = value
			return result
		}
	}
	return result
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }
