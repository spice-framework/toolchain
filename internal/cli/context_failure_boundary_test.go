package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spice-framework/toolchain/compiler/load"
)

func TestLoadModuleVersionsUsesOfflineTargetModuleGraph(t *testing.T) {
	root, err := generatedModuleRoot(".")
	if err != nil {
		t.Fatal(err)
	}
	// A named nil value deliberately verifies the documented normalization to
	// context.Background without tripping staticcheck's accidental-nil guard.
	var nilContext context.Context
	modules, err := loadModuleVersions(nilContext, load.Options{
		Dir: root,
		Env: os.Environ(),
	})
	if err != nil {
		t.Fatalf("loadModuleVersions() error = %v", err)
	}
	foundMain := false
	for _, module := range modules {
		if module.Main && module.Path == "github.com/spice-framework/toolchain" {
			foundMain = true
			break
		}
	}
	if !foundMain {
		t.Fatalf("module graph has no toolchain main module: %+v", modules)
	}
}

func TestLoadModuleVersionsReportsGoListFailure(t *testing.T) {
	t.Parallel()

	_, err := loadModuleVersions(t.Context(), load.Options{Dir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "inspect offline Go module graph") {
		t.Fatalf("loadModuleVersions() error = %v", err)
	}
}

func TestResolvePatternsContextPreservesLoaderAndWriterFailures(t *testing.T) {
	t.Parallel()

	want := errors.New("load canceled")
	loader := func(ctx context.Context, _ load.Options, _ ...string) (*load.Program, error) {
		if !errors.Is(ctx.Err(), context.Canceled) {
			t.Fatalf("loader context error = %v", ctx.Err())
		}
		return nil, want
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var stderr bytes.Buffer
	program, result, ok := resolvePatternsContext(
		ctx,
		[]string{"./..."},
		&stderr,
		load.Options{},
		loader,
		"context analysis",
	)
	if ok || program != nil || len(result.Occurrences) != 0 ||
		!strings.Contains(stderr.String(), want.Error()) {
		t.Fatalf("resolvePatternsContext() = %+v, %+v, %t, stderr=%q", program, result, ok, stderr.String())
	}
	if _, _, ok := resolvePatternsContext(
		ctx,
		nil,
		errorWriter{},
		load.Options{},
		loader,
		"context analysis",
	); ok {
		t.Fatal("resolvePatternsContext() succeeded with a failed diagnostic writer")
	}
}

func TestLegacyStarterSelectionFailsBeforeCompilerConstruction(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	legacy := filepath.Join(root, filepath.FromSlash(legacyStarterSelectionPath))
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := rejectLegacyStarterSelection(load.Options{Dir: root}); err == nil ||
		!strings.Contains(err.Error(), "retired starter selection") {
		t.Fatalf("rejectLegacyStarterSelection() error = %v", err)
	}
	if service, err := newCompilerAnalysisService(
		load.Options{Dir: root},
		load.Load,
	); service != nil || err == nil {
		t.Fatalf("newCompilerAnalysisService() = %v, %v", service, err)
	}
	if _, _, ready := prepareGenerationContext(
		t.Context(),
		generationArguments{},
		errorWriter{},
		load.Options{Dir: root},
		load.Load,
	); ready {
		t.Fatal("prepareGenerationContext() succeeded with retired starter selection")
	}
}

func TestLSPCommandRejectsArgumentsAndInvalidStreams(t *testing.T) {
	t.Parallel()

	loader := func(context.Context, load.Options, ...string) (*load.Program, error) {
		return nil, errors.New("unexpected loader invocation")
	}
	var stderr bytes.Buffer
	if code := lspCommandContext(
		t.Context(),
		[]string{"unexpected"},
		strings.NewReader(""),
		&bytes.Buffer{},
		&stderr,
		load.Options{},
		loader,
	); code != 2 || !strings.Contains(stderr.String(), "does not accept arguments") {
		t.Fatalf("argument rejection code=%d stderr=%q", code, stderr.String())
	}
	if code := lspCommandContext(
		t.Context(),
		[]string{"unexpected"},
		strings.NewReader(""),
		&bytes.Buffer{},
		errorWriter{},
		load.Options{},
		loader,
	); code != 1 {
		t.Fatalf("argument writer failure code = %d", code)
	}

	stderr.Reset()
	if code := lspCommandContext(
		t.Context(),
		nil,
		nil,
		&bytes.Buffer{},
		&stderr,
		load.Options{},
		loader,
	); code != 1 || !strings.Contains(stderr.String(), "input and output must not be nil") {
		t.Fatalf("stream rejection code=%d stderr=%q", code, stderr.String())
	}
}

func TestModuleGraphEnvironmentUsesProcessEnvironmentWhenNil(t *testing.T) {
	t.Setenv("GOPROXY", "https://proxy.invalid")
	t.Setenv("GOSUMDB", "sum.invalid")
	environment := moduleGraphEnvironment(nil)
	if !slices.Contains(environment, "GOPROXY=off") ||
		!slices.Contains(environment, "GOSUMDB=off") {
		t.Fatalf("offline environment = %v", environment)
	}
	for _, value := range environment {
		if value == "GOPROXY=https://proxy.invalid" || value == "GOSUMDB=sum.invalid" {
			t.Fatalf("online module setting retained: %q", value)
		}
	}
}
