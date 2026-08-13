package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

func TestRunRejectsInvalidArguments(t *testing.T) {
	t.Parallel()
	valid := []string{
		"-artifacts", "missing", "-verified-output", "verified", "-root", "missing",
		"-repository", "spice-agent-coding",
		"-source", "https://github.com/spice-framework/spice-agent-coding",
		"-module", "github.com/spice-framework/spice-agent-coding",
		"-version", "v0.1.0-preview.4", "-commit", strings.Repeat("a", 40),
		"-profile", "go-distribution-v1",
	}
	for _, test := range []struct {
		name      string
		arguments []string
		wantCode  int
		wantError string
	}{
		{name: "unknown flag", arguments: []string{"-unknown"}, wantCode: 2, wantError: "flag provided"},
		{name: "positional", arguments: []string{"extra"}, wantCode: 2, wantError: "unexpected arguments"},
		{name: "missing required", wantCode: 2, wantError: "are required"},
		{name: "missing checkout", arguments: valid, wantCode: 1, wantError: "trusted repository"},
		{name: "stale preview.1", arguments: replaceDistributionArgument(valid, "-version", "v0.1.0-preview.1"), wantCode: 1, wantError: "do not match"},
		{name: "stale preview.2", arguments: replaceDistributionArgument(valid, "-version", "v0.1.0-preview.2"), wantCode: 1, wantError: "do not match"},
		{name: "stale preview.3", arguments: replaceDistributionArgument(valid, "-version", "v0.1.0-preview.3"), wantCode: 1, wantError: "do not match"},
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

func TestRunSelectsExactToolchainPolicyBeforeSourceInspection(t *testing.T) {
	t.Parallel()
	valid := []string{
		"-artifacts", "missing", "-verified-output", "verified", "-root", "missing",
		"-repository", "toolchain",
		"-source", "https://github.com/spice-framework/toolchain",
		"-module", "github.com/spice-framework/toolchain",
		"-version", "v0.1.0-preview.8", "-commit", strings.Repeat("a", 40),
		"-profile", "go-distribution-v1",
	}
	for _, test := range []struct {
		name      string
		arguments []string
		want      string
	}{
		{name: "exact policy", arguments: valid, want: "trusted repository"},
		{name: "stale preview.1", arguments: replaceDistributionArgument(valid, "-version", "v0.1.0-preview.1"), want: "do not match"},
		{name: "stale preview.2", arguments: replaceDistributionArgument(valid, "-version", "v0.1.0-preview.2"), want: "do not match"},
		{name: "stale preview.3", arguments: replaceDistributionArgument(valid, "-version", "v0.1.0-preview.3"), want: "do not match"},
		{name: "stale preview.4", arguments: replaceDistributionArgument(valid, "-version", "v0.1.0-preview.4"), want: "do not match"},
		{name: "stale preview.5", arguments: replaceDistributionArgument(valid, "-version", "v0.1.0-preview.5"), want: "do not match"},
		{name: "stale preview.6", arguments: replaceDistributionArgument(valid, "-version", "v0.1.0-preview.6"), want: "do not match"},
		{name: "stale preview.7", arguments: replaceDistributionArgument(valid, "-version", "v0.1.0-preview.7"), want: "do not match"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			code := run(context.Background(), test.arguments, &stdout, &stderr)
			if code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("run(Toolchain %s) = %d, stdout %q, stderr %q", test.name, code, stdout.String(), stderr.String())
			}
		})
	}
}

func replaceDistributionArgument(arguments []string, flagName, value string) []string {
	result := append([]string{}, arguments...)
	for index := range len(result) - 1 {
		if result[index] == flagName {
			result[index+1] = value
			return result
		}
	}
	return result
}

func TestWriteDistributionExitHandlesWriterFailure(t *testing.T) {
	t.Parallel()
	if code := writeDistributionExit(distributionFailingWriter{}, 2, "failure"); code != 1 {
		t.Fatalf("writeDistributionExit() = %d", code)
	}
}

type distributionFailingWriter struct{}

func (distributionFailingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }
