package boundarygate

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spice-framework/toolchain/internal/identity"
)

func TestVerifyExercisesTheStandaloneRepositoryContract(t *testing.T) {
	root := t.TempDir()
	writeGateFile(t, root, "main.go", "package toolchain\n")
	writeGateFile(t, root, "vendor/example.txt", "vendored\n")
	if err := os.MkdirAll(filepath.Join(root, "testdata", "annotationapp"), 0o750); err != nil {
		t.Fatal(err)
	}

	var calls []string
	execute := func(
		_ context.Context,
		directory string,
		environment map[string]string,
		executable string,
		arguments ...string,
	) ([]byte, error) {
		calls = append(calls, executable+" "+strings.Join(arguments, " "))
		switch {
		case executable == "go" && slices.Equal(arguments, []string{"version"}):
			return []byte("go version go1.26.5 windows/amd64\n"), nil
		case executable == "go" && len(arguments) >= 5 && slices.Equal(arguments[:4], []string{"tool", "-C", "tools", "-n"}):
			return []byte(arguments[4] + "\n"), nil
		case executable == "goimports" || executable == "gofumpt":
			return nil, nil
		case executable == "go" && len(arguments) >= 4 && slices.Equal(arguments[:3], []string{"mod", "vendor", "-o"}):
			writeGateFile(t, arguments[3], "example.txt", "vendored\n")
			return nil, nil
		case executable == "go" && len(arguments) >= 2 && arguments[0] == "tool" && arguments[1] == "cover":
			return []byte("total:\t(statements)\t85.2%\n"), nil
		case executable == "go" && slices.Equal(arguments, []string{"tool", identity.CLITool, "run", ".", "./component", "--", "-check"}):
			return []byte("fixture ready.\n"), nil
		case executable == "go" && len(arguments) >= 3 && slices.Equal(arguments[:3], []string{"tool", identity.CLITool, "generate"}):
			writeGateFile(t, directory, ".spice/app.manifest.json", "{}\n")
			writeGateFile(t, directory, "internal/spicegen/app.go", "package spicegen\n")
			return nil, nil
		default:
			if environment != nil && environment["GOPROXY"] == "off" && environment["GOWORK"] != "off" {
				t.Fatalf("offline command environment = %#v", environment)
			}
			return nil, nil
		}
	}

	var output bytes.Buffer
	gate := verifier{root: root, output: &output, execute: execute}
	if err := gate.verify(context.Background()); err != nil {
		t.Fatalf("verify() error = %v", err)
	}
	if !strings.Contains(output.String(), "Standalone Spice toolchain verification passed.") {
		t.Fatalf("verify() output = %q", output.String())
	}
	for _, want := range []string{
		"go vet ./...",
		"go test -race -shuffle=on -count=1",
		"go test -mod=vendor -count=1 ./...",
		"go tool " + identity.CLITool + " generate . ./component",
		"go tool " + identity.CLITool + " verify . ./component",
	} {
		if !containsCall(calls, want) {
			t.Errorf("verification calls do not contain %q:\n%s", want, strings.Join(calls, "\n"))
		}
	}
	for _, relative := range []string{".spice", "bin", "internal/spicegen"} {
		if _, err := os.Stat(filepath.Join(root, "testdata", "annotationapp", filepath.FromSlash(relative))); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("fixture output %s remains after verification: %v", relative, err)
		}
	}
}

func TestFastAndCheckStopAtCommandFailures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		run  func(verifier) error
		fail string
	}{
		{
			name: "fast test",
			run: func(gate verifier) error {
				return gate.fast(context.Background())
			},
			fail: "go test -count=1",
		},
		{
			name: "check broad test",
			run: func(gate verifier) error {
				return gate.check(context.Background())
			},
			fail: "second go test",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeGateFile(t, root, "main.go", "package toolchain\n")
			failure := errors.New("command failed")
			testCalls := 0
			gate := verifier{
				root:   root,
				output: io.Discard,
				execute: func(_ context.Context, _ string, _ map[string]string, executable string, arguments ...string) ([]byte, error) {
					call := executable + " " + strings.Join(arguments, " ")
					if call == "go version" {
						return []byte("go version go1.26.5 windows/amd64\n"), nil
					}
					if strings.HasPrefix(call, "go test") {
						testCalls++
					}
					if strings.HasPrefix(call, test.fail) || (test.fail == "second go test" && testCalls == 2) {
						return nil, failure
					}
					return nil, nil
				},
			}
			if err := test.run(gate); !errors.Is(err, failure) {
				t.Fatalf("run() error = %v, want %v", err, failure)
			}
		})
	}
}

func TestVerificationFailureReportsTheStep(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	gate := verifier{
		root:   t.TempDir(),
		output: &output,
		execute: func(context.Context, string, map[string]string, string, ...string) ([]byte, error) {
			return []byte("go version go1.26.4 windows/amd64\n"), nil
		},
	}
	err := gate.verify(context.Background())
	if err == nil || !strings.Contains(err.Error(), "Go version") {
		t.Fatalf("verify() error = %v", err)
	}
	if output.String() != "==> Go version\n    go version\n" {
		t.Fatalf("verify() output = %q", output.String())
	}
}

func TestFormattingReportsStableFileList(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeGateFile(t, root, "z.go", "package z\n")
	writeGateFile(t, root, "a.go", "package a\n")
	gate := verifier{
		root:   root,
		output: io.Discard,
		execute: func(_ context.Context, _ string, _ map[string]string, executable string, arguments ...string) ([]byte, error) {
			if executable == "go" {
				return []byte(arguments[len(arguments)-1] + "\n"), nil
			}
			if executable == "goimports" {
				return []byte("z.go\na.go\n"), nil
			}
			return nil, nil
		},
	}
	err := gate.formatting(context.Background())
	if err == nil || err.Error() != "goimports required for a.go, z.go" {
		t.Fatalf("formatting() error = %v", err)
	}
}

func TestVendorAndBootstrapBoundariesRejectDrift(t *testing.T) {
	t.Parallel()
	t.Run("vendor content", func(t *testing.T) {
		t.Parallel()
		root := t.TempDir()
		writeGateFile(t, root, "vendor/committed.txt", "committed\n")
		gate := verifier{
			root:   root,
			output: io.Discard,
			execute: func(_ context.Context, _ string, _ map[string]string, _ string, arguments ...string) ([]byte, error) {
				writeGateFile(t, arguments[3], "generated.txt", "different\n")
				return nil, nil
			},
		}
		if err := gate.vendorReproducibility(context.Background()); err == nil || !strings.Contains(err.Error(), "vendor differs") {
			t.Fatalf("vendorReproducibility() error = %v", err)
		}
	})
	t.Run("generated bootstrap dependency", func(t *testing.T) {
		t.Parallel()
		gate := verifier{
			root:   t.TempDir(),
			output: io.Discard,
			execute: func(context.Context, string, map[string]string, string, ...string) ([]byte, error) {
				return []byte(identity.ToolchainModule + "/internal/spicegen/application\n"), nil
			},
		}
		if err := gate.bootstrapDependencies(context.Background()); err == nil || !strings.Contains(err.Error(), "depends on production generated code") {
			t.Fatalf("bootstrapDependencies() error = %v", err)
		}
	})
}

func TestThirdPartyGenerationRejectsMissingNondeterministicAndUnreadyOutput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		fake func(*testing.T, string, []string, int) ([]byte, error)
		want string
	}{
		{
			name: "missing output",
			fake: func(*testing.T, string, []string, int) ([]byte, error) {
				return nil, nil
			},
			want: "first generation produced no owned output",
		},
		{
			name: "nondeterministic output",
			fake: func(t *testing.T, directory string, arguments []string, generation int) ([]byte, error) {
				t.Helper()
				if len(arguments) >= 3 && arguments[2] == "generate" {
					writeGateFile(t, directory, ".spice/app.manifest.json", strings.Repeat("x", generation))
				}
				return nil, nil
			},
			want: "generation from zero is not byte-for-byte deterministic",
		},
		{
			name: "not ready",
			fake: func(t *testing.T, directory string, arguments []string, _ int) ([]byte, error) {
				t.Helper()
				if len(arguments) >= 3 && arguments[2] == "generate" {
					writeGateFile(t, directory, ".spice/app.manifest.json", "stable")
				}
				return []byte("not started\n"), nil
			},
			want: "fixture readiness output",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			app := filepath.Join(t.TempDir(), "testdata", "annotationapp")
			if err := os.MkdirAll(app, 0o750); err != nil {
				t.Fatal(err)
			}
			generations := 0
			gate := verifier{
				root:   filepath.Dir(filepath.Dir(app)),
				output: io.Discard,
				execute: func(_ context.Context, directory string, _ map[string]string, _ string, arguments ...string) ([]byte, error) {
					if len(arguments) >= 3 && arguments[2] == "generate" && (len(arguments) < 4 || arguments[3] != "--check") {
						generations++
					}
					return test.fake(t, directory, arguments, generations)
				},
			}
			if err := gate.thirdPartyGeneration(context.Background()); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("thirdPartyGeneration() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestCommandExecutesAndIncludesFailureOutput(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	gate := verifier{root: t.TempDir(), output: &output}
	version, err := gate.capture(context.Background(), gate.root, nil, "go", "version")
	if err != nil || !bytes.Contains(version, []byte(requiredGoVersion)) {
		t.Fatalf("capture(go version) = %q, %v", version, err)
	}
	_, err = gate.capture(context.Background(), gate.root, nil, "go", "list", "./package-that-does-not-exist")
	if err == nil || !strings.Contains(err.Error(), "package-that-does-not-exist") {
		t.Fatalf("capture(failure) error = %v", err)
	}
	if !strings.Contains(output.String(), "go version") || !strings.Contains(output.String(), "package-that-does-not-exist") {
		t.Fatalf("command progress output = %q", output.String())
	}
}

func TestGoVersionAndToolPathValidateCommandOutput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		output string
		call   func(verifier) error
		want   string
	}{
		{
			name:   "short version",
			output: "go version",
			call: func(gate verifier) error {
				return gate.goVersion(context.Background())
			},
			want: "require exactly go1.26.5",
		},
		{
			name:   "wrong version",
			output: "go version go1.26.4 windows/amd64",
			call: func(gate verifier) error {
				return gate.goVersion(context.Background())
			},
			want: "require exactly go1.26.5",
		},
		{
			name: "empty tool path",
			call: func(gate verifier) error {
				_, err := gate.toolPath(context.Background(), "gofumpt")
				return err
			},
			want: `resolve pinned tool "gofumpt": empty path`,
		},
		{
			name:   "multiple tool paths",
			output: "first\nsecond\n",
			call: func(gate verifier) error {
				_, err := gate.toolPath(context.Background(), "gofumpt")
				return err
			},
			want: `resolve pinned tool "gofumpt": expected one path, got 2`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			gate := verifier{
				root:   t.TempDir(),
				output: io.Discard,
				execute: func(context.Context, string, map[string]string, string, ...string) ([]byte, error) {
					return []byte(test.output), nil
				},
			}
			if err := test.call(gate); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("call() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestToolPathUsesOnlyStdoutFromCleanCacheResolution(t *testing.T) {
	t.Parallel()
	const executable = `C:\Program Files\Go Tools\goimports.exe`
	gate := verifier{
		root:   t.TempDir(),
		output: io.Discard,
		executeStreams: func(context.Context, string, map[string]string, string, ...string) ([]byte, []byte, error) {
			return []byte(executable + "\r\n"), []byte("go: downloading golang.org/x/tools v0.48.0\n"), nil
		},
	}

	got, err := gate.toolPath(context.Background(), "goimports")
	if err != nil {
		t.Fatal(err)
	}
	if got != executable {
		t.Fatalf("toolPath() = %q, want %q", got, executable)
	}
}

func TestCommandFailureIncludesBoundedStdoutAndStderr(t *testing.T) {
	t.Parallel()
	gate := verifier{
		root:   t.TempDir(),
		output: io.Discard,
		executeStreams: func(context.Context, string, map[string]string, string, ...string) ([]byte, []byte, error) {
			return []byte("stdout detail"), bytes.Repeat([]byte("stderr detail"), maxCommandFailureOutput), errors.New("exit status 1")
		},
	}

	_, err := gate.capture(context.Background(), gate.root, nil, "tool", "argument")
	if err == nil {
		t.Fatal("capture() error = nil")
	}
	if !strings.Contains(err.Error(), "[command output truncated]") || !strings.Contains(err.Error(), "stderr detail") {
		t.Fatalf("capture() error = %q", err)
	}
	if len(err.Error()) > maxCommandFailureOutput+256 {
		t.Fatalf("capture() error length = %d", len(err.Error()))
	}
}

func TestCoverageRejectsMalformedAndLowReports(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		report string
		want   string
	}{
		{name: "missing total", report: "example.go 80.0%", want: "no total row"},
		{name: "invalid total", report: "total: (statements) no%", want: "parse total coverage"},
		{name: "below floor", report: "total: (statements) 84.9%", want: "below 85.0%"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			gate := verifier{
				root:   t.TempDir(),
				output: io.Discard,
				execute: func(_ context.Context, _ string, _ map[string]string, executable string, arguments ...string) ([]byte, error) {
					if executable == "go" && len(arguments) >= 2 && arguments[0] == "tool" && arguments[1] == "cover" {
						return []byte(test.report), nil
					}
					return nil, nil
				},
			}
			if err := gate.coverage(context.Background()); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("coverage() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestFilesystemHelpersPreserveIdentityAndRejectUnsafeTrees(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeGateFile(t, root, "z.go", "package z\n")
	writeGateFile(t, root, "a.go", "package a\n")
	writeGateFile(t, root, "vendor/ignored.go", "package ignored\n")
	writeGateFile(t, root, ".spice/ignored.go", "package ignored\n")
	writeGateFile(t, root, "internal/spicegen/ignored.go", "package ignored\n")

	gate := verifier{root: root, output: io.Discard}
	files, err := gate.goFiles()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a.go", "z.go"}; !slices.Equal(files, want) {
		t.Fatalf("goFiles() = %v, want %v", files, want)
	}

	first, err := snapshotTree(root)
	if err != nil {
		t.Fatal(err)
	}
	second := make(map[string][32]byte, len(first))
	for path, digest := range first {
		second[path] = digest
	}
	if !snapshotsEqual(first, second) {
		t.Fatal("equal snapshots differ")
	}
	delete(second, "a.go")
	if snapshotsEqual(first, second) {
		t.Fatal("snapshots with different paths compare equal")
	}
	second = make(map[string][32]byte, len(first))
	for path, digest := range first {
		second[path] = digest
	}
	second["a.go"] = [32]byte{1}
	if snapshotsEqual(first, second) {
		t.Fatal("snapshots with different content compare equal")
	}

	missing, err := snapshotTree(filepath.Join(root, "missing"))
	if err != nil || len(missing) != 0 {
		t.Fatalf("snapshotTree(missing) = %v, %v", missing, err)
	}
	link := filepath.Join(root, "unsafe-link")
	symlinkErr := os.Symlink(filepath.Join(root, "a.go"), link)
	if symlinkErr == nil {
		_, snapshotErr := snapshotTree(root)
		if snapshotErr == nil || !strings.Contains(snapshotErr.Error(), "snapshot contains symlink") {
			t.Fatalf("snapshotTree(symlink) error = %v", snapshotErr)
		}
		if removeErr := os.Remove(link); removeErr != nil {
			t.Fatal(removeErr)
		}
	}

	owned, err := snapshotOwned([]string{root, filepath.Join(root, "missing")})
	if err != nil {
		t.Fatal(err)
	}
	if len(owned) == 0 {
		t.Fatal("snapshotOwned() is empty")
	}
}

func TestMergedEnvironmentOverridesCaseInsensitively(t *testing.T) {
	t.Setenv("SPICE_BOUNDARY_GATE_TEST", "old")
	merged := mergedEnvironment(map[string]string{"spice_boundary_gate_test": "new", "SPICE_ONLY_OVERRIDE": "value"})
	values := make(map[string]string)
	for _, item := range merged {
		name, value, found := strings.Cut(item, "=")
		if found {
			values[strings.ToUpper(name)] = value
		}
	}
	if values["SPICE_BOUNDARY_GATE_TEST"] != "new" || values["SPICE_ONLY_OVERRIDE"] != "value" {
		t.Fatalf("mergedEnvironment() = %#v", values)
	}
}

func TestIdentityBoundaryRejectsMovedMonorepositoryImports(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeGateFile(t, root, "bad.go", `package bad

import "github.com/spice-framework/spice/compiler/load"
`)
	gate := verifier{root: root, output: io.Discard}
	if err := gate.identityBoundary(); err == nil || !strings.Contains(err.Error(), "stale monorepository identities") {
		t.Fatalf("identityBoundary() error = %v", err)
	}
}

func TestIdentityBoundaryAcceptsPublicCoreImports(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeGateFile(t, root, "good.go", `package good

import "github.com/spice-framework/spice/annotation/sdk"
`)
	gate := verifier{root: root, output: io.Discard}
	if err := gate.identityBoundary(); err != nil {
		t.Fatalf("identityBoundary() error = %v", err)
	}
}

func TestRunRejectsUnknownModeAndNilContext(t *testing.T) {
	t.Parallel()
	if err := Run(nil, t.TempDir(), "fast", io.Discard); err == nil { //nolint:staticcheck // Nil contract is under test.
		t.Fatal("Run(nil) error = nil")
	}
	if err := Run(context.Background(), t.TempDir(), "unknown", io.Discard); err == nil {
		t.Fatal("Run(unknown) error = nil")
	}
}

func TestTotalCoverageReadsGoToolReport(t *testing.T) {
	t.Parallel()
	coverage, err := totalCoverage("example.go:1:\tfunction\t50.0%\ntotal:\t(statements)\t85.7%\n")
	if err != nil || coverage != 85.7 {
		t.Fatalf("totalCoverage() = %.1f, %v", coverage, err)
	}
	if _, err := totalCoverage("no total row"); err == nil {
		t.Fatal("totalCoverage(missing) error = nil")
	}
}

func containsCall(calls []string, want string) bool {
	return slices.ContainsFunc(calls, func(call string) bool {
		return strings.Contains(call, want)
	})
}

func writeGateFile(t *testing.T, root, relative, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
