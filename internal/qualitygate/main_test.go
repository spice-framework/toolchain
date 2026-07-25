package main

import (
	"context"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestVerifyOrchestration(t *testing.T) {
	silenceOutput(t)
	root := t.TempDir()
	writeTestFile(t, root, "go.mod", "module "+modulePath+"\n\ngo 1.26.0\n")
	writeTestFile(t, root, "tools/go.mod", "module "+modulePath+"/tools\n\ngo 1.26.0\n")
	writeTestFile(t, root, "main.go", "package main\n")
	writeTestFile(t, root, "vendor/modules.txt", "# test vendor tree\n")

	originalRun, originalCapture := runExternal, captureExternal
	var calls []string
	runExternal = func(
		_ context.Context,
		directory string,
		_ map[string]string,
		executable string,
		arguments ...string,
	) error {
		calls = append(calls, executable+" "+strings.Join(arguments, " "))
		for index, argument := range arguments {
			if argument == "-o" && index+1 < len(arguments) {
				copyTestTree(t, filepath.Join(directory, "vendor"), arguments[index+1])
			}
		}
		return nil
	}
	captureExternal = func(
		_ context.Context,
		_ string,
		executable string,
		arguments ...string,
	) (string, error) {
		calls = append(calls, executable+" "+strings.Join(arguments, " "))
		switch {
		case slices.Equal(arguments, []string{"version"}):
			return "go version " + requiredGoVersion + " test/arch\n", nil
		case len(arguments) >= 2 && arguments[0] == "tool" && arguments[1] == "cover":
			return "total:\t(statements)\t90.0%\n", nil
		case slices.Contains(arguments, "-n"):
			return "go\n", nil
		default:
			return "", nil
		}
	}
	t.Cleanup(func() {
		runExternal = originalRun
		captureExternal = originalCapture
	})

	if err := verify(context.Background(), root); err != nil {
		t.Fatalf("verify() error = %v", err)
	}
	if err := format(context.Background(), root, true); err != nil {
		t.Fatalf("format(write=true) error = %v", err)
	}
	for _, expected := range []string{
		"go vet ./...",
		"run --timeout=10m",
		"-include-pkgs=" + modulePath,
		"-race -shuffle=on",
		"-coverprofile=",
		"-mod=vendor -count=1",
		"verify ./...",
	} {
		if !containsCall(calls, expected) {
			t.Fatalf("calls do not contain %q:\n%s", expected, strings.Join(calls, "\n"))
		}
	}
}

func TestRepositoryAndFilesystemHelpers(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Fatalf("repositoryRoot() error = %v", err)
	}
	data, err := readRootFile(root, "go.mod")
	if err != nil {
		t.Fatalf("readRootFile() error = %v", err)
	}
	if !strings.Contains(string(data), "module "+modulePath) {
		t.Fatalf("go.mod = %q", data)
	}

	tree := t.TempDir()
	writeTestFile(t, tree, "a.go", "package a\n")
	writeTestFile(t, tree, "nested/b.go", "package nested\n")
	writeTestFile(t, tree, "vendor/ignored.go", "package ignored\n")
	writeTestFile(t, tree, ".git/ignored.go", "package ignored\n")
	files, err := goFiles(tree)
	if err != nil {
		t.Fatalf("goFiles() error = %v", err)
	}
	if !slices.Equal(files, []string{"a.go", filepath.Join("nested", "b.go")}) {
		t.Fatalf("goFiles() = %#v", files)
	}

	first, err := treeDigest(tree)
	if err != nil {
		t.Fatalf("treeDigest() error = %v", err)
	}
	second, err := treeDigest(tree)
	if err != nil {
		t.Fatalf("treeDigest() second error = %v", err)
	}
	if !equalDigests(first, second) {
		t.Fatal("identical trees have different digests")
	}
	second["different"] = [32]byte{1}
	if equalDigests(first, second) {
		t.Fatal("different trees have equal digests")
	}
}

func TestCoverageAndExecutableHelpers(t *testing.T) {
	total, err := totalCoverage("file.go:1:\tFunction\t50.0%\ntotal:\t(statements)\t87.5%\n")
	if err != nil || total != 87.5 {
		t.Fatalf("totalCoverage() = %v, %v", total, err)
	}
	if _, coverageErr := totalCoverage("no total"); coverageErr == nil {
		t.Fatal("totalCoverage() error = nil")
	}
	for _, executable := range []string{"go", "gofumpt", "goimports", "golangci-lint", "gosec", "govulncheck", "nilaway"} {
		if executableErr := validateExecutable(executable); executableErr != nil {
			t.Fatalf("validateExecutable(%q) error = %v", executable, executableErr)
		}
	}
	if executableErr := validateExecutable("curl"); executableErr == nil {
		t.Fatal("validateExecutable(curl) error = nil")
	}

	stdout, err := capture(context.Background(), t.TempDir(), "go", "version")
	if err != nil || !strings.Contains(stdout, requiredGoVersion) {
		t.Fatalf("capture(go version) = %q, %v", stdout, err)
	}
	if err := command(context.Background(), t.TempDir(), nil, "unapproved"); err == nil {
		t.Fatal("command(unapproved) error = nil")
	}
}

func TestEnvironmentAndCleanupHelpers(t *testing.T) {
	environment := mergedEnvironment(map[string]string{"SPICE_QUALITY_TEST": "present"})
	if !slices.Contains(environment, "SPICE_QUALITY_TEST=present") {
		t.Fatalf("mergedEnvironment() missing override: %#v", environment)
	}
	want := "Spice_Test"
	if runtime.GOOS == "windows" {
		want = "SPICE_TEST"
	}
	if got := environmentKey("Spice_Test"); got != want {
		t.Fatalf("environmentKey() = %q, want %q", got, want)
	}

	path := filepath.Join(t.TempDir(), "temporary")
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	removeTemporaryDirectory(path)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary directory still exists: %v", err)
	}
}

func TestCheckGoVersionAndUnknownMode(t *testing.T) {
	root, err := repositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	originalCapture := captureExternal
	captureExternal = func(
		_ context.Context,
		_ string,
		_ string,
		arguments ...string,
	) (string, error) {
		if slices.Equal(arguments, []string{"version"}) {
			return "go version go0.0.1 test/arch", nil
		}
		return "", nil
	}
	t.Cleanup(func() { captureExternal = originalCapture })
	if err := checkGoVersion(context.Background(), root); err == nil {
		t.Fatal("checkGoVersion() error = nil for wrong version")
	}
	captureExternal = func(
		_ context.Context,
		_ string,
		_ string,
		arguments ...string,
	) (string, error) {
		if slices.Equal(arguments, []string{"version"}) {
			return "go version " + requiredGoVersion + " test/arch", nil
		}
		return "", nil
	}
	if err := run(context.Background(), "unknown"); err == nil {
		t.Fatal("run(unknown) error = nil")
	}
}

func TestRunModesWithFakeExternal(t *testing.T) {
	silenceOutput(t)
	originalRun, originalCapture := runExternal, captureExternal
	runExternal = func(
		_ context.Context,
		directory string,
		_ map[string]string,
		_ string,
		arguments ...string,
	) error {
		for index, argument := range arguments {
			if argument == "-o" && index+1 < len(arguments) {
				copyTestTree(t, filepath.Join(directory, "vendor"), arguments[index+1])
			}
		}
		return nil
	}
	captureExternal = func(
		_ context.Context,
		_ string,
		_ string,
		arguments ...string,
	) (string, error) {
		switch {
		case slices.Equal(arguments, []string{"version"}):
			return "go version " + requiredGoVersion + " test/arch", nil
		case len(arguments) >= 2 && arguments[0] == "tool" && arguments[1] == "cover":
			return "total:\t(statements)\t90.0%\n", nil
		case slices.Contains(arguments, "-n"):
			return "go\n", nil
		default:
			return "", nil
		}
	}
	t.Cleanup(func() {
		runExternal = originalRun
		captureExternal = originalCapture
	})

	for _, mode := range []string{"fmt", "fuzz", "lint", "security", "smoke", "test", "vet", "offline", "verify"} {
		if err := run(context.Background(), mode); err != nil {
			t.Fatalf("run(%q) error = %v", mode, err)
		}
	}
}

func TestQualityGateFailurePaths(t *testing.T) {
	root := t.TempDir()
	writeTestFile(t, root, "go.mod", "module "+modulePath+"\n\ngo 1.26.0\n")
	writeTestFile(t, root, "tools/go.mod", "module "+modulePath+"/tools\n\ngo 1.26.0\n")
	writeTestFile(t, root, "main.go", "package main\n")
	writeTestFile(t, root, "vendor/modules.txt", "# committed\n")

	originalRun, originalCapture := runExternal, captureExternal
	t.Cleanup(func() {
		runExternal = originalRun
		captureExternal = originalCapture
	})

	sentinel := errors.New("external failure")
	runExternal = func(context.Context, string, map[string]string, string, ...string) error {
		return sentinel
	}
	captureExternal = func(context.Context, string, string, ...string) (string, error) {
		return "", sentinel
	}
	if err := checkGoVersion(context.Background(), root); !errors.Is(err, sentinel) {
		t.Fatalf("checkGoVersion() error = %v", err)
	}
	if err := fileBatches(context.Background(), root, "go", "-w", []string{"main.go"}); !errors.Is(err, sentinel) {
		t.Fatalf("fileBatches() error = %v", err)
	}
	if err := test(context.Background(), root); !errors.Is(err, sentinel) {
		t.Fatalf("test() error = %v", err)
	}
	if err := smoke(context.Background(), root); !errors.Is(err, sentinel) {
		t.Fatalf("smoke() error = %v", err)
	}

	runExternal = func(context.Context, string, map[string]string, string, ...string) error {
		return nil
	}
	captureExternal = func(
		_ context.Context,
		_ string,
		_ string,
		arguments ...string,
	) (string, error) {
		switch {
		case slices.Contains(arguments, "-n"):
			return "go\n", nil
		case len(arguments) >= 2 && arguments[0] == "mod" && arguments[1] == "tidy":
			return "go.mod differs", nil
		case len(arguments) >= 2 && arguments[0] == "tool" && arguments[1] == "cover":
			return "total:\t(statements)\t84.9%\n", nil
		default:
			return "main.go\n", nil
		}
	}
	if err := format(context.Background(), root, false); err == nil {
		t.Fatal("format(check) error = nil for unformatted file")
	}
	if err := checkModuleTidy(context.Background(), root); err == nil {
		t.Fatal("checkModuleTidy() error = nil for diff")
	}
	if err := coverage(context.Background(), root); err == nil {
		t.Fatal("coverage() error = nil below floor")
	}

	captureExternal = func(context.Context, string, string, ...string) (string, error) {
		return "", nil
	}
	if _, err := toolPath(context.Background(), root, "missing"); err == nil {
		t.Fatal("toolPath() error = nil for empty path")
	}
	if err := checkVendor(context.Background(), root); err == nil {
		t.Fatal("checkVendor() error = nil for missing generated vendor tree")
	}
	if _, err := treeDigest(filepath.Join(root, "missing")); err == nil {
		t.Fatal("treeDigest() error = nil for missing root")
	}

	if err := command(context.Background(), root, nil, "go", "version"); err != nil {
		t.Fatalf("command(go version) error = %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := capture(cancelled, root, "go", "version"); err == nil {
		t.Fatal("capture(cancelled) error = nil")
	}
}

func writeTestFile(t *testing.T, root, path, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func copyTestTree(t *testing.T, source, target string) {
	t.Helper()
	err := filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(destination, data, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func containsCall(calls []string, fragment string) bool {
	for _, call := range calls {
		if strings.Contains(call, fragment) {
			return true
		}
	}
	return false
}

func silenceOutput(t *testing.T) {
	t.Helper()
	original := output
	output = log.New(io.Discard, "", 0)
	t.Cleanup(func() { output = original })
}
