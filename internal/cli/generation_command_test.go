package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/StevenBuglione/spice/compiler/application"
	"github.com/StevenBuglione/spice/compiler/load"
)

func TestRunGenerateApplyCheckAndDiff(t *testing.T) {
	root := generationCLIModule(t, generationApplicationSource)

	code, stdout, stderr := runModule(root, "generate", "./...")
	if code != 0 || !strings.Contains(stdout, "Spice generated target Application") || stderr != "" {
		t.Fatalf("generate: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	generatedPath := filepath.Join(root, "internal", "spicegen", "application", "zz_spice_gen.go")
	manifestPath := filepath.Join(root, ".spice", "application.manifest.json")
	if _, err := os.Stat(generatedPath); err != nil {
		t.Fatalf("generated file: %v", err)
	}
	if _, err := os.Stat(manifestPath); err != nil {
		t.Fatalf("manifest: %v", err)
	}
	code, stdout, stderr = runModule(root, "generate", "./...")
	if code != 0 || !strings.Contains(stdout, "is current") || stderr != "" {
		t.Fatalf("second generate: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	code, stdout, stderr = runModule(root, "generate", "--check", "./...")
	if code != 0 || !strings.Contains(stdout, "is current") || stderr != "" {
		t.Fatalf("generate --check: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	if err := os.WriteFile(
		filepath.Join(root, "app", "application.go"),
		[]byte(generationApplicationWithProviderSource),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = runModule(root, "generate", "--diff", "./...")
	if code != 1 ||
		!strings.Contains(stdout, "+\tprovider0 := app.ValueProvider()") ||
		!strings.Contains(stderr, "generation is stale") {
		t.Fatalf("generate --diff: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	after, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatal("read-only generation diff changed generated source")
	}

	code, stdout, stderr = runModule(root, "generate", "./...")
	if code != 0 || !strings.Contains(stdout, "wrote 1 file") || stderr != "" {
		t.Fatalf("regenerate: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	content, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "app.ValueProvider()") {
		t.Fatalf("regenerated source missing provider:\n%s", content)
	}

	const renamedProvider = `package app

type Value struct{}

// @Bean
func RenamedProvider() Value { return Value{} }

// @Application
func Application(Value) {}
`
	if writeErr := os.WriteFile(
		filepath.Join(root, "app", "application.go"),
		[]byte(renamedProvider),
		0o600,
	); writeErr != nil {
		t.Fatal(writeErr)
	}
	code, stdout, stderr = runModule(root, "generate", "./...")
	if code != 0 || !strings.Contains(stdout, "wrote 1 file") || stderr != "" {
		t.Fatalf("recover stale uncompilable output: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	content, err = os.ReadFile(generatedPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "app.RenamedProvider()") {
		t.Fatalf("recovered generated source missing renamed provider:\n%s", content)
	}
}

func TestRunBuildGeneratesAndExecutesTrimpathBuild(t *testing.T) {
	root := generationCLIModule(t, generationApplicationWithProviderSource)
	code, stdout, stderr := runModule(root, "build", "./...")
	if code != 0 ||
		!strings.Contains(stdout, "Spice build passed for target Application") ||
		stderr != "" {
		t.Fatalf("build: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runModule(root, "build", "./...")
	if code != 0 ||
		strings.Contains(stdout, "wrote") ||
		!strings.Contains(stdout, "Spice build passed") ||
		stderr != "" {
		t.Fatalf("second build: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestRunBuildReportsExecutorFailure(t *testing.T) {
	root := generationCLIModule(t, generationApplicationSource)
	sentinel := errors.New("builder failed")
	called := false
	builder := func(
		_ context.Context,
		directory string,
		_ io.Writer,
		_ io.Writer,
	) error {
		called = true
		if directory != root {
			t.Fatalf("build directory = %q, want %q", directory, root)
		}
		return sentinel
	}
	var stdout, stderr bytes.Buffer
	code := runWithBuilder(
		[]string{"build", "./..."},
		&stdout,
		&stderr,
		load.Options{Dir: root},
		load.Load,
		builder,
	)
	if code != 1 || !called || !strings.Contains(stderr.String(), sentinel.Error()) {
		t.Fatalf("code=%d called=%t stdout=%q stderr=%q", code, called, stdout.String(), stderr.String())
	}
}

func TestRunGenerateSelectsOneOfMultipleApplications(t *testing.T) {
	root := generationCLIModule(t, `package app

// @Application
func Alpha() {}

// @Application
func Beta() {}
`)
	code, stdout, stderr := runModule(root, "generate", "./...")
	if code != 1 ||
		stdout != "" ||
		!strings.Contains(stderr, "multiple @Application targets") ||
		!strings.Contains(stderr, "--target") {
		t.Fatalf("ambiguous: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	code, stdout, stderr = runModule(root, "generate", "--target", "Beta", "./...")
	if code != 0 || !strings.Contains(stdout, "target Beta") || stderr != "" {
		t.Fatalf("selected: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if _, err := os.Stat(filepath.Join(root, "internal", "spicegen", "beta", "zz_spice_gen.go")); err != nil {
		t.Fatalf("selected generated file: %v", err)
	}

	code, _, stderr = runModule(root, "generate", "--target=Missing", "./...")
	if code != 1 || !strings.Contains(stderr, `target "Missing" was not found`) {
		t.Fatalf("missing target: code=%d stderr=%q", code, stderr)
	}
}

func TestRunGenerateRequiresApplicationAndValidOptions(t *testing.T) {
	root := generationCLIModule(t, "package app\n")
	code, stdout, stderr := runModule(root, "generate", "./...")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "no @Application marker") {
		t.Fatalf("no application: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	tests := [][]string{
		{"generate", "--unknown"},
		{"generate", "--target"},
		{"generate", "--target=First", "--target=Second"},
		{"build", "--check"},
		{"build", "--diff"},
	}
	for _, arguments := range tests {
		code, _, stderr := runModule(root, arguments...)
		if code != 2 || stderr == "" {
			t.Errorf("%v: code=%d stderr=%q", arguments, code, stderr)
		}
	}
}

func TestRunGenerateReportsCompilerAndFilesystemFailures(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name: "annotation validation",
			source: `package app

// @Application(value=true)
func Application() {}
`,
			want: "annotation validation error",
		},
		{
			name: "application model",
			source: `package app

// @Bean
func Broken() error { return nil }

// @Application
func Application() {}
`,
			want: "provider catalog error",
		},
		{
			name: "render visibility",
			source: `package app

type Value struct{}

// @Bean
func privateProvider() Value { return Value{} }

// @Application
func Application(Value) {}
`,
			want: "require exported @Bean functions",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := generationCLIModule(t, test.source)
			code, stdout, stderr := runModule(root, "generate", "./...")
			if code != 1 || stdout != "" || !strings.Contains(stderr, test.want) {
				t.Fatalf("code=%d stdout=%q stderr=%q, want %q", code, stdout, stderr, test.want)
			}
		})
	}

	root := generationCLIModule(t, generationApplicationSource)
	code, _, _ := runModule(root, "generate", "./...")
	if code != 0 {
		t.Fatal("initial generation failed")
	}
	manifestPath := filepath.Join(root, ".spice", "application.manifest.json")
	if err := os.WriteFile(manifestPath, []byte("{broken}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runModule(root, "generate", "--check", "./...")
	if code != 1 || stdout != "" || !strings.Contains(stderr, "decode ownership manifest") {
		t.Fatalf("malformed manifest: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	loaderFailure := errors.New("loader failed")
	loader := func(context.Context, load.Options, ...string) (*load.Program, error) {
		return nil, loaderFailure
	}
	var output, errorOutput bytes.Buffer
	code = runWithBuilder(
		[]string{"generate"},
		&output,
		&errorOutput,
		load.Options{},
		loader,
		executeGoBuild,
	)
	if code != 1 || !strings.Contains(errorOutput.String(), loaderFailure.Error()) {
		t.Fatalf("loader failure: code=%d stdout=%q stderr=%q", code, output.String(), errorOutput.String())
	}
}

func TestSelectApplicationTargetSupportsStableIdentityAndAmbiguity(t *testing.T) {
	t.Parallel()
	targets := []application.Target{
		{Name: "Web", SymbolID: "web-id"},
		{Name: "Worker", SymbolID: "worker-id"},
	}
	target, err := selectApplicationTarget(targets, "worker-id")
	if err != nil || target.Name != "Worker" {
		t.Fatalf("selectApplicationTarget() = %#v, %v", target, err)
	}
	if _, err := selectApplicationTarget(
		[]application.Target{{Name: "Web"}, {Name: "web"}},
		"WEB",
	); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("case-fold ambiguity error = %v", err)
	}
}

func TestWithAnalysisBuildTagPreservesCallerTags(t *testing.T) {
	t.Parallel()
	options := load.Options{
		Env: []string{
			"GOFLAGS=-mod=mod -tags=environment,shared",
		},
		BuildFlags: []string{
			"-gcflags=all=-N",
			"-tags",
			"local,shared",
		},
	}
	result := withAnalysisBuildTag(options)
	want := []string{
		"-gcflags=all=-N",
		"-tags=environment,local,shared,spice_generate",
	}
	if !slices.Equal(result.BuildFlags, want) {
		t.Fatalf("BuildFlags = %v, want %v", result.BuildFlags, want)
	}
	if got := goFlags([]string{"OTHER=value"}); got != "" {
		t.Fatalf("goFlags(no GOFLAGS) = %q", got)
	}
}

const generationApplicationSource = `package app

// @Application
func Application() {}
`

const generationApplicationWithProviderSource = `package app

type Value struct{}

// @Bean
func ValueProvider() Value { return Value{} }

// @Application
func Application(Value) {}
`

func generationCLIModule(t *testing.T, source string) string {
	t.Helper()
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return writeModule(t, map[string]string{
		"go.mod": "module example.com/cli-generation\n\ngo 1.26.0\n\n" +
			"require github.com/StevenBuglione/spice v0.0.0\n\n" +
			"replace github.com/StevenBuglione/spice => " + filepath.ToSlash(repository) + "\n",
		"app/application.go": source,
	})
}
