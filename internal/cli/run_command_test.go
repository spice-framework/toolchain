package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/StevenBuglione/spice/compiler/load"
)

func TestRunCommandBuildsAndExecutesPreferredApplication(t *testing.T) {
	root := packageMainRunModule(t)

	code, stdout, stderr := runModule(root, "run", "--", "-check")
	if code != 0 ||
		!strings.Contains(stdout, "Spice runfixture ready.") ||
		!strings.Contains(stderr, "Spice generated target Runfixture") ||
		!strings.Contains(stderr, "Spice running target Runfixture.") {
		t.Fatalf(
			"run: code=%d stdout=%q stderr=%q",
			code,
			stdout,
			stderr,
		)
	}
	for _, relative := range []string{
		"zz_spice_bridge_gen.go",
		"internal/spicegen/runfixture/zz_spice_gen.go",
		".spice/runfixture.manifest.json",
	} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(relative))); err != nil {
			t.Fatalf("generated %s: %v", relative, err)
		}
	}

	code, stdout, stderr = runModule(root, "run", "--", "-unknown")
	if code != 2 ||
		stdout != "" ||
		!strings.Contains(stderr, "flag provided but not defined") {
		t.Fatalf(
			"invalid application arguments: code=%d stdout=%q stderr=%q",
			code,
			stdout,
			stderr,
		)
	}
}

func TestRunCommandPreservesArgumentsExitCodeAndCleansArtifact(t *testing.T) {
	root := packageMainRunModule(t)
	var executable string
	builder := func(
		ctx context.Context,
		directory string,
		packagePath string,
		outputPath string,
		_ io.Writer,
		_ io.Writer,
	) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if directory != root || packagePath != "example.com/runfixture" {
			t.Fatalf(
				"build target = %q, %q",
				directory,
				packagePath,
			)
		}
		executable = outputPath
		return os.WriteFile(outputPath, []byte("candidate"), 0o700)
	}
	executor := func(
		ctx context.Context,
		outputPath string,
		arguments []string,
		_ io.Reader,
		_ io.Writer,
		_ io.Writer,
	) (int, error) {
		if err := ctx.Err(); err != nil {
			return 1, err
		}
		if outputPath != executable {
			t.Fatalf("executable = %q, want %q", outputPath, executable)
		}
		if !strings.HasSuffix(executable, applicationExecutableSuffix()) {
			t.Fatalf("executable suffix = %q", executable)
		}
		if strings.Join(arguments, "\x00") != "one\x00two" {
			t.Fatalf("arguments = %q", arguments)
		}
		if _, err := os.Stat(outputPath); err != nil {
			t.Fatalf("candidate executable: %v", err)
		}
		return 23, nil
	}

	var stdout, stderr bytes.Buffer
	code := runCommandWithExecutors(
		context.Background(),
		[]string{"--", "one", "two"},
		bytes.NewReader(nil),
		&stdout,
		&stderr,
		load.Options{Dir: root},
		load.Load,
		builder,
		executor,
	)
	if code != 23 || stdout.String() != "" {
		t.Fatalf(
			"code=%d stdout=%q stderr=%q",
			code,
			stdout.String(),
			stderr.String(),
		)
	}
	if _, err := os.Stat(executable); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temporary executable remains: %v", err)
	}
}

func TestRunCommandRejectsLegacyApplicationBeforeBuild(t *testing.T) {
	root := generationCLIModule(t, generationApplicationSource)
	called := false
	builder := func(
		context.Context,
		string,
		string,
		string,
		io.Writer,
		io.Writer,
	) error {
		called = true
		return nil
	}
	executor := func(
		context.Context,
		string,
		[]string,
		io.Reader,
		io.Writer,
		io.Writer,
	) (int, error) {
		t.Fatal("legacy application was executed")
		return 1, nil
	}
	var stdout, stderr bytes.Buffer
	code := runCommandWithExecutors(
		context.Background(),
		[]string{"./..."},
		bytes.NewReader(nil),
		&stdout,
		&stderr,
		load.Options{Dir: root},
		load.Load,
		builder,
		executor,
	)
	if code != 1 ||
		called ||
		stdout.String() != "" ||
		!strings.Contains(stderr.String(), "migrate the marker to func main") {
		t.Fatalf(
			"code=%d called=%t stdout=%q stderr=%q",
			code,
			called,
			stdout.String(),
			stderr.String(),
		)
	}
}

func TestRunCommandReportsBuildAndExecutionFailures(t *testing.T) {
	root := packageMainRunModule(t)
	buildFailure := errors.New("build failed")
	executionFailure := errors.New("execution failed")
	tests := []struct {
		name     string
		builder  applicationBuildExecutor
		executor applicationRunExecutor
		want     error
	}{
		{
			name: "build",
			builder: func(
				context.Context,
				string,
				string,
				string,
				io.Writer,
				io.Writer,
			) error {
				return buildFailure
			},
			executor: func(
				context.Context,
				string,
				[]string,
				io.Reader,
				io.Writer,
				io.Writer,
			) (int, error) {
				t.Fatal("executor called after build failure")
				return 1, nil
			},
			want: buildFailure,
		},
		{
			name: "execution",
			builder: func(
				_ context.Context,
				_ string,
				_ string,
				outputPath string,
				_ io.Writer,
				_ io.Writer,
			) error {
				return os.WriteFile(outputPath, nil, 0o700)
			},
			executor: func(
				context.Context,
				string,
				[]string,
				io.Reader,
				io.Writer,
				io.Writer,
			) (int, error) {
				return 1, executionFailure
			},
			want: executionFailure,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := runCommandWithExecutors(
				context.Background(),
				nil,
				bytes.NewReader(nil),
				&stdout,
				&stderr,
				load.Options{Dir: root},
				load.Load,
				test.builder,
				test.executor,
			)
			if code != 1 ||
				!strings.Contains(stderr.String(), test.want.Error()) {
				t.Fatalf(
					"code=%d stdout=%q stderr=%q",
					code,
					stdout.String(),
					stderr.String(),
				)
			}
		})
	}
}

func TestParseRunArguments(t *testing.T) {
	t.Parallel()
	parsed, err := parseRunArguments([]string{
		"--target",
		"Shop",
		"./cmd/shop/...",
		"--",
		"-check",
		"value",
	})
	if err != nil {
		t.Fatal(err)
	}
	if parsed.generation.target != "Shop" ||
		strings.Join(parsed.generation.patterns, ",") != "./cmd/shop/..." ||
		strings.Join(parsed.application, ",") != "-check,value" {
		t.Fatalf("parsed = %#v", parsed)
	}
	for _, arguments := range [][]string{
		{"--check"},
		{"--diff"},
		{"--target"},
		{"--unknown"},
	} {
		if _, err := parseRunArguments(arguments); err == nil {
			t.Errorf("parseRunArguments(%q) error = nil", arguments)
		}
	}
}

func BenchmarkParseRunArguments(b *testing.B) {
	arguments := []string{
		"--target",
		"Commerce",
		"./examples/commerce/...",
		"--",
		"-check",
	}
	b.ReportAllocs()
	for b.Loop() {
		parsed, err := parseRunArguments(arguments)
		if err != nil {
			b.Fatal(err)
		}
		if parsed.generation.target != "Commerce" {
			b.Fatalf("target = %q", parsed.generation.target)
		}
	}
}

func TestExecuteApplicationHonorsPreCanceledContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	code, err := executeApplication(
		ctx,
		filepath.Join(t.TempDir(), "missing"),
		nil,
		bytes.NewReader(nil),
		io.Discard,
		io.Discard,
	)
	if code != 1 || !errors.Is(err, context.Canceled) {
		t.Fatalf("executeApplication() = %d, %v", code, err)
	}
}

func TestValidateApplicationPackagePath(t *testing.T) {
	t.Parallel()
	if err := validateApplicationPackagePath("example.com/app"); err != nil {
		t.Fatal(err)
	}
	for _, packagePath := range []string{"", "-replace=bad"} {
		if err := validateApplicationPackagePath(packagePath); err == nil {
			t.Errorf("validateApplicationPackagePath(%q) error = nil", packagePath)
		}
	}
	suffix := applicationExecutableSuffix()
	if runtime.GOOS == "windows" && suffix != ".exe" {
		t.Fatalf("Windows suffix = %q", suffix)
	}
	if runtime.GOOS != "windows" && suffix != "" {
		t.Fatalf("%s suffix = %q", runtime.GOOS, suffix)
	}
}

func packageMainRunModule(t *testing.T) string {
	t.Helper()
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return writeModule(t, map[string]string{
		"go.mod": "module example.com/runfixture\n\ngo 1.26.0\n\n" +
			"require github.com/StevenBuglione/spice v0.0.0\n\n" +
			"replace github.com/StevenBuglione/spice => " +
			filepath.ToSlash(repository) + "\n",
		"main.go": `package main

import "os"

// @Application
func main() {
	os.Exit(spiceMain(os.Args[1:]))
}
`,
	})
}
