package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRejectsInvalidArgumentsAndArtifacts(t *testing.T) {
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
			name: "missing key",
			arguments: []string{
				"-artifacts", "missing", "-repository", "starter-test",
				"-source", "https://github.com/spice-framework/starter-test",
				"-module", "github.com/spice-framework/starter-test", "-version", "v1.2.3",
				"-commit", strings.Repeat("a", 40), "-trusted-public-key", "missing.pem",
			},
			wantCode: 1, wantError: "read trusted public key",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var stdout, stderr bytes.Buffer
			if code := run(context.Background(), test.arguments, &stdout, &stderr); code != test.wantCode ||
				!strings.Contains(stderr.String(), test.wantError) {
				t.Fatalf("run() = %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestRunReachesIndependentVerifier(t *testing.T) {
	t.Parallel()
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "release.pub")
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	arguments := []string{
		"-artifacts", filepath.Join(t.TempDir(), "missing"),
		"-root", t.TempDir(),
		"-repository", "starter-test",
		"-source", "https://github.com/spice-framework/starter-test",
		"-module", "github.com/spice-framework/starter-test",
		"-version", "v1.2.3",
		"-commit", strings.Repeat("a", 40),
		"-trusted-public-key", keyPath,
	}
	var stderr bytes.Buffer
	if code := run(context.Background(), arguments, &bytes.Buffer{}, &stderr); code != 1 ||
		!strings.Contains(stderr.String(), "read library artifact directory") {
		t.Fatalf("run() = %d, stderr %q", code, stderr.String())
	}
}

func TestReadBoundedFile(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	filename := filepath.Join(directory, "key")
	if err := os.WriteFile(filename, []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := readBoundedFile(filename, 3)
	if err != nil || string(data) != "key" {
		t.Fatalf("readBoundedFile(valid) = %q, %v", data, err)
	}
	if _, err := readBoundedFile(filename, 2); err == nil {
		t.Fatal("readBoundedFile(oversized) error = nil")
	}
	if _, err := readBoundedFile(directory, 3); err == nil {
		t.Fatal("readBoundedFile(directory) error = nil")
	}
	link := filepath.Join(directory, "link")
	if err := os.Symlink("key", link); err != nil {
		t.Logf("symlink boundary unavailable on this host: %v", err)
	} else if _, err := readBoundedFile(link, 3); err == nil {
		t.Fatal("readBoundedFile(symlink) error = nil")
	}
}

func TestWriteExitHandlesWriterFailure(t *testing.T) {
	t.Parallel()
	if code := writeExit(failingWriter{}, 2, "failure"); code != 1 {
		t.Fatalf("writeExit(failing writer) = %d", code)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, errors.New("write failed")
}
