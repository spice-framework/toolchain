package boundarygate

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

type fakeReleaseSubjectSet struct {
	version     string
	commit      string
	extractErr  error
	extraction  string
	installRoot string
	calls       *[]string
}

func (set *fakeReleaseSubjectSet) Version() string { return set.version }
func (set *fakeReleaseSubjectSet) Commit() string  { return set.commit }
func (set *fakeReleaseSubjectSet) ExtractNativeContext(_ context.Context, destination, goos, goarch string) (string, error) {
	*set.calls = append(*set.calls, "extract "+goos+"/"+goarch)
	set.extraction = destination
	if set.extractErr != nil {
		return "", set.extractErr
	}
	set.installRoot = filepath.Join(destination, "toolchain")
	if err := os.MkdirAll(set.installRoot, 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(set.installRoot, "spice"+executableSuffix()), []byte("fixture"), 0o700); err != nil {
		return "", err
	}
	return set.installRoot, nil
}

func TestReleaseArtifactsVerifyExtractExecuteAndCleanInOrder(t *testing.T) {
	t.Parallel()
	directory := canonicalTestDirectory(t)
	var calls []string
	set := &fakeReleaseSubjectSet{
		version: "v0.1.0-preview.8",
		commit:  strings.Repeat("a", 40),
		calls:   &calls,
	}
	gate := verifier{
		output: io.Discard,
		verifySubjects: func(_ context.Context, actual string) (releaseSubjectSet, error) {
			calls = append(calls, "verify")
			if actual != directory {
				t.Fatalf("verified directory = %q, want %q", actual, directory)
			}
			return set, nil
		},
		executeStreams: func(
			_ context.Context,
			workingDirectory string,
			environment map[string]string,
			executable string,
			arguments ...string,
		) ([]byte, []byte, error) {
			calls = append(calls, "execute")
			if workingDirectory != set.installRoot || executable != filepath.Join(set.installRoot, "spice"+executableSuffix()) {
				t.Fatalf("execution boundary = %q %q", workingDirectory, executable)
			}
			if !slices.Equal(arguments, []string{"--version"}) {
				t.Fatalf("execution arguments = %#v", arguments)
			}
			for name, value := range releaseArtifactOverrides() {
				if environment[name] != value {
					t.Fatalf("environment[%s] = %q", name, environment[name])
				}
			}
			return []byte("spice 0.1.0-preview.8 (" + strings.Repeat("a", 40) + ")\n"), nil, nil
		},
	}
	if err := gate.releaseArtifacts(context.Background(), directory); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(calls, []string{"verify", "extract " + runtimeTarget(), "execute"}) {
		t.Fatalf("release verification order = %#v", calls)
	}
	if _, err := os.Stat(filepath.Dir(set.extraction)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("scratch directory remains: %v", err)
	}
}

func TestReleaseArtifactsFailClosed(t *testing.T) {
	t.Parallel()
	sentinel := errors.New("sentinel")
	tests := []struct {
		name      string
		configure func(*verifier, *fakeReleaseSubjectSet)
		want      string
	}{
		{name: "verifier", configure: func(gate *verifier, _ *fakeReleaseSubjectSet) {
			gate.verifySubjects = func(context.Context, string) (releaseSubjectSet, error) { return nil, sentinel }
		}, want: "verify release subjects"},
		{name: "extract", configure: func(_ *verifier, set *fakeReleaseSubjectSet) { set.extractErr = sentinel }, want: "extract native"},
		{name: "execute", configure: func(gate *verifier, _ *fakeReleaseSubjectSet) {
			gate.executeStreams = func(context.Context, string, map[string]string, string, ...string) ([]byte, []byte, error) {
				return nil, nil, sentinel
			}
		}, want: "execute installed"},
		{name: "stdout", configure: func(gate *verifier, _ *fakeReleaseSubjectSet) {
			gate.executeStreams = func(context.Context, string, map[string]string, string, ...string) ([]byte, []byte, error) {
				return []byte("wrong\n"), nil, nil
			}
		}, want: "identity"},
		{name: "stderr", configure: func(gate *verifier, set *fakeReleaseSubjectSet) {
			gate.executeStreams = func(context.Context, string, map[string]string, string, ...string) ([]byte, []byte, error) {
				return []byte("spice " + strings.TrimPrefix(set.version, "v") + " (" + set.commit + ")\n"), []byte("noise"), nil
			}
		}, want: "stderr"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var calls []string
			set := &fakeReleaseSubjectSet{version: "v0.1.0-preview.8", commit: strings.Repeat("b", 40), calls: &calls}
			gate := verifier{
				output:         io.Discard,
				verifySubjects: func(context.Context, string) (releaseSubjectSet, error) { return set, nil },
				executeStreams: func(context.Context, string, map[string]string, string, ...string) ([]byte, []byte, error) {
					return []byte("spice 0.1.0-preview.8 (" + strings.Repeat("b", 40) + ")\n"), nil, nil
				},
			}
			test.configure(&gate, set)
			err := gate.releaseArtifacts(context.Background(), canonicalTestDirectory(t))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("releaseArtifacts() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestReleaseArtifactsRespectCancellationAndPathBoundaries(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	gate := verifier{verifySubjects: func(context.Context, string) (releaseSubjectSet, error) {
		called = true
		return nil, errors.New("unexpected verifier call")
	}}
	if err := gate.releaseArtifacts(ctx, canonicalTestDirectory(t)); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled releaseArtifacts() error = %v", err)
	}
	if called {
		t.Fatal("release subjects were inspected after cancellation")
	}
	for _, path := range []string{"", ".", canonicalTestDirectory(t) + string(filepath.Separator) + ".." + string(filepath.Separator) + "artifacts"} {
		if err := gate.releaseArtifacts(context.Background(), path); err == nil || !strings.Contains(err.Error(), "canonical and absolute") {
			t.Fatalf("releaseArtifacts(%q) error = %v", path, err)
		}
	}
}

func TestReleaseArtifactEnvironmentIsOfflineSortedAndSecretFree(t *testing.T) {
	t.Parallel()
	actual := releaseArtifactEnvironment([]string{
		"PATH=/bin", "GOOS=plan9", "GOFLAGS=-tags=wrong", "API_TOKEN=hidden", "SIGNING_KEY=hidden", "ZED=last",
	}, releaseArtifactOverrides())
	if !slices.IsSorted(actual) {
		t.Fatalf("release environment is not sorted: %#v", actual)
	}
	joined := strings.Join(actual, "\n")
	for _, forbidden := range []string{"PATH=", "plan9", "-tags=wrong", "hidden", "TOKEN", "SIGNING_KEY"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("release environment contains %q: %s", forbidden, joined)
		}
	}
	for name, value := range releaseArtifactOverrides() {
		if !strings.Contains(joined, name+"="+value) {
			t.Fatalf("release environment lacks %s=%s: %s", name, value, joined)
		}
	}
}

func TestReleaseArtifactOutputIsBounded(t *testing.T) {
	t.Parallel()
	buffer := newBoundedBuffer(4)
	if written, err := buffer.Write([]byte("1234")); err != nil || written != 4 {
		t.Fatalf("boundary write = %d, %v", written, err)
	}
	if written, err := buffer.Write([]byte("5")); err == nil || written != 0 {
		t.Fatalf("overflow write = %d, %v", written, err)
	}
	if _, _, err := checkedReleaseArtifactOutput(make([]byte, maxCommandFailureOutput+1), nil, nil); err == nil {
		t.Fatal("oversized mocked output was accepted")
	}
}

func TestReleaseArtifactEntrypointIsExact(t *testing.T) {
	t.Parallel()
	valid := validReleaseArtifactMakefile()
	tests := []struct {
		name   string
		mutate func(string) string
	}{
		{name: "missing phony", mutate: func(value string) string { return strings.Replace(value, " "+releaseArtifactTarget, "", 1) }},
		{name: "wrong input", mutate: func(value string) string { return strings.Replace(value, releaseArtifactInput, "ARTIFACTS", 1) }},
		{name: "wrong mode", mutate: func(value string) string { return strings.Replace(value, "release-artifacts", "verify", 1) }},
		{name: "duplicate", mutate: func(value string) string {
			return value + "\n" + releaseArtifactTarget + ": fallback\n\t@echo bypass\n"
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeGateFile(t, root, "Makefile", test.mutate(valid))
			if err := (verifier{root: root}).releaseArtifactEntrypoint(); err == nil {
				t.Fatal("mutated release artifact entrypoint succeeded")
			}
		})
	}
	root := t.TempDir()
	writeGateFile(t, root, "Makefile", valid)
	if err := (verifier{root: root}).releaseArtifactEntrypoint(); err != nil {
		t.Fatal(err)
	}
}

func validReleaseArtifactMakefile() string {
	return ".PHONY: fast " + releaseArtifactTarget + "\n\n" + releaseArtifactTarget + ":\n\tgo run ./internal/boundarygate/cmd -mode=release-artifacts -artifacts=\"$(" + releaseArtifactInput + ")\"\n"
}

func writeValidReleaseArtifactEntrypoint(t *testing.T, root string) {
	t.Helper()
	writeGateFile(t, root, "Makefile", validCandidateMakefile())
	writeGateFile(t, root, ".github/workflows/ci.yml", validCandidateCIWorkflow())
}

func canonicalTestDirectory(t *testing.T) string {
	t.Helper()
	directory, err := filepath.Abs(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(directory)
}

func runtimeTarget() string {
	return runtime.GOOS + "/" + runtime.GOARCH
}
