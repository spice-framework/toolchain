package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRunRejectsInvalidInvocationBeforeBuilding(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		arguments []string
		wantCode  int
		wantError string
	}{
		{
			name:      "missing version",
			wantCode:  2,
			wantError: "-version is required",
		},
		{
			name:      "invalid target",
			arguments: []string{"-version=v1.0.0", "-targets=plan9/amd64"},
			wantCode:  2,
			wantError: "unsupported target",
		},
		{
			name:      "positional argument",
			arguments: []string{"unexpected"},
			wantCode:  2,
			wantError: "unexpected arguments",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var stdout bytes.Buffer
			var stderr bytes.Buffer
			code := run(
				t.Context(),
				test.arguments,
				&stdout,
				&stderr,
			)
			if code != test.wantCode ||
				!strings.Contains(stderr.String(), test.wantError) ||
				stdout.Len() != 0 {
				t.Fatalf(
					"run() code=%d stdout=%q stderr=%q",
					code,
					stdout.String(),
					stderr.String(),
				)
			}
		})
	}
}

func TestSourceEpochUsesExplicitValue(t *testing.T) {
	t.Parallel()
	got, err := sourceEpoch(t.Context(), t.TempDir(), 1_788_000_000)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Unix(1_788_000_000, 0).UTC()
	if !got.Equal(want) {
		t.Fatalf("sourceEpoch() = %s, want %s", got, want)
	}
}

func TestRunBuildsHostRehearsal(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "release")
	key := make([]byte, 32)
	for index := range key {
		key[index] = byte(index + 1)
	}
	keyFile := filepath.Join(t.TempDir(), "release.key")
	if err := os.WriteFile(
		keyFile,
		[]byte(base64.StdEncoding.EncodeToString(key)),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(
		t.Context(),
		[]string{
			"-rehearsal",
			"-root", root,
			"-output", output,
			"-version", "v0.8.0-rc.1",
			"-source-date-epoch", "1788000000",
			"-targets", defaultHostTarget(),
			"-signing-key", keyFile,
		},
		&stdout,
		&stderr,
	)
	if code != 0 ||
		!strings.Contains(stdout.String(), "created 5 artifact(s)") ||
		stderr.Len() != 0 {
		t.Fatalf(
			"run() code=%d stdout=%q stderr=%q",
			code,
			stdout.String(),
			stderr.String(),
		)
	}
	if _, err := os.Stat(filepath.Join(output, "checksums.txt")); err != nil {
		t.Fatal(err)
	}
}

func TestSourceEpochUsesEnvironmentAndGitCommit(t *testing.T) {
	t.Setenv("SOURCE_DATE_EPOCH", "1788000001")
	got, err := sourceEpoch(t.Context(), t.TempDir(), 0)
	if err != nil || got.Unix() != 1_788_000_001 {
		t.Fatalf("sourceEpoch(environment) = %s, %v", got, err)
	}
	t.Setenv("SOURCE_DATE_EPOCH", "invalid")
	if _, parseErr := sourceEpoch(
		t.Context(),
		t.TempDir(),
		0,
	); parseErr == nil {
		t.Fatal("invalid SOURCE_DATE_EPOCH was accepted")
	}
	t.Setenv("SOURCE_DATE_EPOCH", "")
	root := t.TempDir()
	runGit(t, root, "init")
	runGit(t, root, "config", "user.name", "Spice Test")
	runGit(t, root, "config", "user.email", "spice@example.test")
	if writeErr := os.WriteFile(
		filepath.Join(root, "tracked.txt"),
		[]byte("stable\n"),
		0o644,
	); writeErr != nil {
		t.Fatal(writeErr)
	}
	runGit(t, root, "add", "tracked.txt")
	runGit(t, root, "commit", "-m", "fixture")
	got, err = sourceEpoch(t.Context(), root, 0)
	if err != nil || got.IsZero() {
		t.Fatalf("sourceEpoch(git) = %s, %v", got, err)
	}
}

func TestReadSigningKeyBoundaries(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	valid := filepath.Join(root, "valid.key")
	if err := os.WriteFile(valid, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if data, err := readSigningKey(valid); err != nil ||
		string(data) != "key" {
		t.Fatalf("readSigningKey() = %q, %v", data, err)
	}
	if _, err := readSigningKey(filepath.Join(root, "missing")); err == nil {
		t.Fatal("missing signing key was accepted")
	}
	oversized := filepath.Join(root, "oversized.key")
	if err := os.WriteFile(oversized, make([]byte, (1<<20)+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readSigningKey(oversized); err == nil {
		t.Fatal("oversized signing key was accepted")
	}
}

func TestWriteExitReportsWriterFailure(t *testing.T) {
	t.Parallel()
	if code := writeExit(errorWriter{}, 2, "failure\n"); code != 1 {
		t.Fatalf("writeExit() = %d, want 1", code)
	}
}

func TestValidateReleaseCheckoutRequiresCleanExactTag(t *testing.T) {
	root := t.TempDir()
	runGit(t, root, "init")
	runGit(t, root, "config", "user.name", "Spice Test")
	runGit(t, root, "config", "user.email", "spice@example.test")
	filename := filepath.Join(root, "tracked.txt")
	if err := os.WriteFile(filename, []byte("stable\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "tracked.txt")
	runGit(t, root, "commit", "-m", "fixture")
	if err := validateReleaseCheckout(
		t.Context(),
		root,
		"v1.0.0",
	); err == nil {
		t.Fatal("untagged checkout was accepted")
	}
	runGit(t, root, "tag", "v1.0.0")
	if err := validateReleaseCheckout(
		t.Context(),
		root,
		"v1.0.0",
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateReleaseCheckout(
		t.Context(),
		root,
		"v1.0.0",
	); err == nil {
		t.Fatal("dirty checkout was accepted")
	}
}

func defaultHostTarget() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}

func runGit(t *testing.T, root string, arguments ...string) {
	t.Helper()
	command := exec.CommandContext(
		context.Background(),
		"git",
		arguments...,
	)
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", arguments, err, output)
	}
}
