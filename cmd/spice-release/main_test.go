package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
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
		{
			name: "invalid semantic version",
			arguments: []string{
				"-rehearsal",
				"-version=1.0.0",
			},
			wantCode:  2,
			wantError: "not canonical semantic version",
		},
		{
			name: "noncanonical semantic version",
			arguments: []string{
				"-rehearsal",
				"-version=v1.0",
			},
			wantCode:  2,
			wantError: "not canonical semantic version",
		},
		{
			name: "signed rehearsal",
			arguments: []string{
				"-rehearsal",
				"-version=v1.0.0",
				"-signing-key=unused",
			},
			wantCode:  2,
			wantError: "cannot be combined",
		},
		{
			name:      "unsigned production",
			arguments: []string{"-version=v1.0.0"},
			wantCode:  2,
			wantError: "signing-key is required",
		},
		{
			name: "build metadata production",
			arguments: []string{
				"-version=v1.0.0+build.1",
				"-signing-key=unused",
			},
			wantCode:  2,
			wantError: "must not contain build metadata",
		},
		{
			name: "mismatched frozen generator version",
			arguments: []string{
				"-version=v0.1.0-preview.1",
				"-signing-key=unused",
			},
			wantCode:  2,
			wantError: "does not match frozen generator version",
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

func TestValidateReleaseIntentAllowsCanonicalPrerelease(t *testing.T) {
	t.Parallel()
	if err := validateReleaseIntent("v0.1.0-preview.2", "private-key", false); err != nil {
		t.Fatalf("validateReleaseIntent() error = %v", err)
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
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(
		t.Context(),
		[]string{
			"-rehearsal",
			"-root", root,
			"-output", output,
			"-version", "v0.1.0-preview.2",
			"-source-date-epoch", "1788000000",
			"-targets", defaultHostTarget(),
		},
		&stdout,
		&stderr,
	)
	if code != 0 ||
		!strings.Contains(stdout.String(), "created 4 artifact(s)") ||
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
	if _, err := os.Stat(filepath.Join(output, "spice_0.1.0-preview.2_source.tar.gz")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(output, "checksums.txt.sig")); !os.IsNotExist(err) {
		t.Fatalf("unsigned rehearsal signature error = %v", err)
	}
}

func TestRunBuildsSignedProductionFromExactTag(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "repository")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "init")
	runGit(t, root, "config", "user.name", "Spice Test")
	runGit(t, root, "config", "user.email", "spice@example.test")
	files := map[string]string{
		"go.mod":    "module github.com/spice-framework/toolchain\n\ngo 1.26.0\n",
		"LICENSE":   "license\n",
		"README.md": "readme\n",
		"cmd/spice/main.go": `package main

func main() {}
`,
		"vendor/modules.txt": "",
	}
	for name, content := range files {
		filename := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(filename), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "release fixture")
	runGit(t, root, "tag", "v0.1.0-preview.2")
	keyFile := filepath.Join(parent, "release.key")
	if err := os.WriteFile(
		keyFile,
		[]byte(base64.StdEncoding.EncodeToString(make([]byte, ed25519.SeedSize))),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(parent, "release")
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := run(
		t.Context(),
		[]string{
			"-root", root,
			"-output", output,
			"-version", "v0.1.0-preview.2",
			"-targets", defaultHostTarget(),
			"-signing-key", keyFile,
		},
		&stdout,
		&stderr,
	)
	if code != 0 ||
		!strings.Contains(stdout.String(), "created 6 artifact(s)") ||
		stderr.Len() != 0 {
		t.Fatalf(
			"run() code=%d stdout=%q stderr=%q",
			code,
			stdout.String(),
			stderr.String(),
		)
	}
	for _, name := range []string{
		"checksums.txt",
		"checksums.txt.pem",
		"checksums.txt.sig",
		"spice_0.1.0-preview.2_source.tar.gz",
	} {
		if _, err := os.Stat(filepath.Join(output, name)); err != nil {
			t.Fatalf("release artifact %q: %v", name, err)
		}
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
	epoch, err := sourceEpoch(t.Context(), root, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, validationErr := validateReleaseCheckout(
		t.Context(),
		root,
		"v1.0.0",
		epoch,
	); validationErr == nil {
		t.Fatal("untagged checkout was accepted")
	}
	runGit(t, root, "tag", "v1.0.0")
	if commit, validationErr := validateReleaseCheckout(
		t.Context(),
		root,
		"v1.0.0",
		epoch,
	); validationErr != nil || commit == "" {
		t.Fatal(validationErr)
	}
	if _, validationErr := validateReleaseCheckout(
		t.Context(),
		root,
		"v1.0.0",
		epoch.Add(time.Second),
	); validationErr == nil || !strings.Contains(validationErr.Error(), "does not match HEAD epoch") {
		t.Fatalf("mismatched epoch error = %v", validationErr)
	}
	runGit(t, root, "commit", "--allow-empty", "-m", "after tag")
	newEpoch, err := sourceEpoch(t.Context(), root, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, validationErr := validateReleaseCheckout(
		t.Context(),
		root,
		"v1.0.0",
		newEpoch,
	); validationErr == nil || !strings.Contains(validationErr.Error(), "does not identify HEAD") {
		t.Fatalf("stale tag error = %v", validationErr)
	}
	runGit(t, root, "tag", "v1.0.1")
	untracked := filepath.Join(root, "untracked.txt")
	if err := os.WriteFile(untracked, []byte("untracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, validationErr := validateReleaseCheckout(
		t.Context(),
		root,
		"v1.0.1",
		newEpoch,
	); validationErr == nil {
		t.Fatal("untracked release checkout was accepted")
	}
	if err := os.Remove(untracked); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, validationErr := validateReleaseCheckout(
		t.Context(),
		root,
		"v1.0.1",
		newEpoch,
	); validationErr == nil {
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
