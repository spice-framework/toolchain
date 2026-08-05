package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/StevenBuglione/spice/compiler/load"
)

func TestScaffoldCommandCreatesApplicationWithoutResolvingModules(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	destination := filepath.Join(parent, "catalog")
	var stdout, stderr strings.Builder
	code := scaffoldCommand(
		[]string{
			"--module", "example.com/acme/catalog",
			"--directory", destination,
			"--spice-version", "v0.2.0",
			"--replace", cliRepositoryRoot(t),
		},
		&stdout,
		&stderr,
	)
	if code != 0 || stderr.String() != "" ||
		!strings.Contains(stdout.String(), "No dependencies were downloaded") {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(destination, "go.sum")); !os.IsNotExist(err) {
		t.Fatalf("scaffold go.sum stat error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(destination, "main.go")); err != nil {
		t.Fatalf("scaffold main.go: %v", err)
	}
}

func TestAddCommandPreviewsThenAppliesExactDependency(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	goMod := "module example.com/add-app\n\ngo 1.26.0\n\n" +
		"replace github.com/StevenBuglione/spice => " +
		filepath.ToSlash(cliRepositoryRoot(t)) + "\n"
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(goMod), 0o600); err != nil {
		t.Fatal(err)
	}
	selector := "github.com/StevenBuglione/spice/bean@v0.0.0"
	options := load.Options{Dir: root, Env: append(os.Environ(), "GOPROXY=off")}
	var previewOutput, previewError strings.Builder
	code := addCommand(
		[]string{selector},
		&previewOutput,
		&previewError,
		options,
	)
	if code != 0 || previewError.String() != "" ||
		!strings.Contains(previewOutput.String(), "Command: go get "+selector) ||
		!strings.Contains(previewOutput.String(), "Preview only") {
		t.Fatalf(
			"preview code=%d stdout=%q stderr=%q",
			code,
			previewOutput.String(),
			previewError.String(),
		)
	}
	if current := readCLIFile(t, filepath.Join(root, "go.mod")); current != goMod {
		t.Fatalf("preview changed go.mod:\n%s", current)
	}
	var applyOutput, applyError strings.Builder
	code = addCommand(
		[]string{"--apply", selector},
		&applyOutput,
		&applyError,
		options,
	)
	if code != 0 || applyError.String() != "" ||
		!strings.Contains(applyOutput.String(), "Applied the previewed module changes") {
		t.Fatalf(
			"apply code=%d stdout=%q stderr=%q",
			code,
			applyOutput.String(),
			applyError.String(),
		)
	}
	if current := readCLIFile(t, filepath.Join(root, "go.mod")); !strings.Contains(
		current,
		"github.com/StevenBuglione/spice v0.0.0",
	) {
		t.Fatalf("applied go.mod:\n%s", current)
	}
}

func TestProjectCommandsRejectAmbiguousInputs(t *testing.T) {
	t.Parallel()
	for name, arguments := range map[string][]string{
		"missing module": {"--directory", "app"},
		"unknown new":    {"--module", "example.com/app", "--mystery"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := parseScaffoldArguments(arguments); err == nil {
				t.Fatal("parseScaffoldArguments() error = nil")
			}
		})
	}
	for name, arguments := range map[string][]string{
		"missing":           nil,
		"floating":          {"example.com/pkg@latest"},
		"multiple":          {"example.com/a@v1.0.0", "example.com/b@v1.0.0"},
		"unknown option":    {"--mystery", "example.com/a@v1.0.0"},
		"missing directory": {"--directory"},
		"invalid path":      {"../pkg@v1.0.0"},
		"multiple at signs": {"example.com/pkg@v1.0.0@other"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := parseAddArguments(arguments, "."); err == nil {
				t.Fatal("parseAddArguments() error = nil")
			}
		})
	}
}

func TestProjectArgumentDefaultsAndOptions(t *testing.T) {
	t.Parallel()
	scaffoldArguments, err := parseScaffoldArguments([]string{
		"--module", "example.com/acme/catalog",
		"--spice-version", "v0.2.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if scaffoldArguments.directory != "catalog" {
		t.Fatalf("default scaffold directory = %q", scaffoldArguments.directory)
	}
	addArguments, err := parseAddArguments([]string{
		"--tool", "--apply", "--directory", "application",
		"example.com/tools/cmd/tool@v1.2.3",
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !addArguments.tool || !addArguments.apply ||
		addArguments.root != "application" ||
		addArguments.path != "example.com/tools/cmd/tool" ||
		addArguments.version != "v1.2.3" {
		t.Fatalf("parseAddArguments() = %#v", addArguments)
	}
}

func TestProjectCommandsReportFailuresWithoutPartialScaffolds(t *testing.T) {
	t.Parallel()
	var stdout, stderr strings.Builder
	if code := scaffoldCommand(
		[]string{"--directory", "missing-module"},
		&stdout,
		&stderr,
	); code != 2 || !strings.Contains(stderr.String(), "--module is required") {
		t.Fatalf("invalid new = code %d, stderr %q", code, stderr.String())
	}
	nonempty := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(nonempty, "developer.txt"),
		[]byte("owned\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	stderr.Reset()
	if code := scaffoldCommand([]string{
		"--module", "example.com/app",
		"--directory", nonempty,
		"--spice-version", "v0.2.0",
	}, &stdout, &stderr); code != 1 ||
		!strings.Contains(stderr.String(), "is not empty") {
		t.Fatalf("owned new = code %d, stderr %q", code, stderr.String())
	}
	created := filepath.Join(t.TempDir(), "writer-failure")
	if code := scaffoldCommand([]string{
		"--module", "example.com/app",
		"--directory", created,
		"--spice-version", "v0.2.0",
	}, errorWriter{}, &stderr); code != 1 {
		t.Fatalf("new output failure code = %d", code)
	}
	stdout.Reset()
	stderr.Reset()
	if code := addCommand(nil, &stdout, &stderr, load.Options{}); code != 2 ||
		!strings.Contains(stderr.String(), "dependency selector") {
		t.Fatalf("invalid add = code %d, stderr %q", code, stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if code := addCommand(
		[]string{"example.com/dependency@v1.0.0"},
		&stdout,
		&stderr,
		load.Options{Dir: t.TempDir()},
	); code != 1 || !strings.Contains(stderr.String(), "Spice add failed") {
		t.Fatalf("uninitialized add = code %d, stderr %q", code, stderr.String())
	}
}

func cliRepositoryRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func readCLIFile(t *testing.T, name string) string {
	t.Helper()
	content, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}
