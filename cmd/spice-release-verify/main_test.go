package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRequiresTrustedInputs(t *testing.T) {
	t.Parallel()
	var stdout, stderr bytes.Buffer
	code := run(t.Context(), nil, &stdout, &stderr)
	if code != 2 || stdout.Len() != 0 ||
		!strings.Contains(stderr.String(), "are required") {
		t.Fatalf("run() = %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
}

func TestRunRejectsUnexpectedArguments(t *testing.T) {
	t.Parallel()
	var stderr bytes.Buffer
	code := run(context.Background(), []string{"extra"}, &bytes.Buffer{}, &stderr)
	if code != 2 || !strings.Contains(stderr.String(), "unexpected arguments") {
		t.Fatalf("run() = %d, stderr %q", code, stderr.String())
	}
}

func TestRunReportsFlagAndTrustedKeyFailures(t *testing.T) {
	t.Parallel()
	t.Run("unknown flag", func(t *testing.T) {
		t.Parallel()
		var stderr bytes.Buffer
		if code := run(t.Context(), []string{"-unknown"}, io.Discard, &stderr); code != 2 ||
			!strings.Contains(stderr.String(), "flag provided but not defined") {
			t.Fatalf("run() = %d, stderr %q", code, stderr.String())
		}
	})
	t.Run("missing key file", func(t *testing.T) {
		t.Parallel()
		var stderr bytes.Buffer
		code := run(t.Context(), []string{
			"-artifacts", t.TempDir(),
			"-version", "v0.1.0",
			"-commit", strings.Repeat("0", 40),
			"-trusted-public-key", filepath.Join(t.TempDir(), "missing.pem"),
		}, io.Discard, &stderr)
		if code != 1 || !strings.Contains(stderr.String(), "read trusted public key") {
			t.Fatalf("run() = %d, stderr %q", code, stderr.String())
		}
	})
	t.Run("invalid trusted key", func(t *testing.T) {
		t.Parallel()
		key := filepath.Join(t.TempDir(), "key.pem")
		if err := os.WriteFile(key, []byte("not a PEM key\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		var stderr bytes.Buffer
		code := run(t.Context(), []string{
			"-artifacts", t.TempDir(),
			"-version", "v0.1.0",
			"-commit", strings.Repeat("0", 40),
			"-trusted-public-key", key,
		}, io.Discard, &stderr)
		if code != 1 || !strings.Contains(stderr.String(), "PEM") {
			t.Fatalf("run() = %d, stderr %q", code, stderr.String())
		}
	})
}

func TestReadBoundedFileRejectsDirectoryAndOversize(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	if _, err := readBoundedFile(directory, 10); err == nil {
		t.Fatal("readBoundedFile(directory) succeeded")
	}
	filename := filepath.Join(directory, "key.pem")
	if err := os.WriteFile(filename, bytes.Repeat([]byte{'x'}, 11), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readBoundedFile(filename, 10); err == nil {
		t.Fatal("readBoundedFile(oversize) succeeded")
	}
	valid := filepath.Join(directory, "valid.pem")
	if err := os.WriteFile(valid, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if data, err := readBoundedFile(valid, 3); err != nil || string(data) != "key" {
		t.Fatalf("readBoundedFile(valid) = %q, %v", data, err)
	}
}

func TestWriteExitReturnsWriterFailure(t *testing.T) {
	t.Parallel()
	if code := writeExit(errorWriter{}, 2, "failure: %s", "detail"); code != 1 {
		t.Fatalf("writeExit() = %d, want 1", code)
	}
}

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}
