package service

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/spice-framework/toolchain/compiler/diagnostic"
	"github.com/spice-framework/toolchain/compiler/load"
	compilerstyle "github.com/spice-framework/toolchain/compiler/style"
)

func TestConfiguredStyleExecutesEveryExactBuildSelection(t *testing.T) {
	root := writeStyleSelectionModule(t, map[string]string{
		"app/worker_linux.go":   "package app\n\ntype LinuxWorker struct{}\n",
		"app/worker_windows.go": "package app\n\ntype WindowsWorker struct{}\n",
	})
	configuration := styleSelectionConfiguration(false)
	trueValue := true
	configuration.BuildSelections[1].CGOEnabled = &trueValue
	type capturedLoad struct {
		options  load.Options
		patterns []string
	}
	var mu sync.Mutex
	var calls []capturedLoad
	loader := func(
		ctx context.Context,
		options load.Options,
		patterns ...string,
	) (*load.Program, error) {
		mu.Lock()
		calls = append(calls, capturedLoad{
			options:  cloneLoadOptions(options),
			patterns: slices.Clone(patterns),
		})
		mu.Unlock()
		return load.Load(ctx, options, patterns...)
	}
	service, err := New(Config{
		Loader: loader,
		LoadOptions: load.Options{
			Env: append(
				os.Environ(),
				"GOFLAGS=-tags=ambient",
				"GOOS=plan9",
				"GOARCH=386",
				"CGO_ENABLED=1",
			),
			BuildFlags: []string{"-tags=ambient", "-gcflags=all=-N"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Analyze(context.Background(), Request{
		WorkspaceRoot:      root,
		Mode:               AnalysisValidate,
		StyleConfiguration: &configuration,
	})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if diagnostics := result.Diagnostics().Items(); len(diagnostics) != 0 {
		t.Fatalf("Analyze() diagnostics = %v", diagnostics)
	}
	if len(calls) != 2 {
		t.Fatalf("loader calls = %d, want 2", len(calls))
	}
	for index, call := range calls {
		selection := configuration.BuildSelections[index]
		if !slices.Equal(call.patterns, []string{"./app/..."}) {
			t.Fatalf("selection %s patterns = %v", selection.Name, call.patterns)
		}
		if environmentValue(call.options.Env, "GOFLAGS") != "" {
			t.Fatalf("selection %s GOFLAGS was not cleared", selection.Name)
		}
		wantedCGO := "0"
		if *selection.CGOEnabled {
			wantedCGO = "1"
		}
		if environmentValue(call.options.Env, "GOOS") != selection.GOOS ||
			environmentValue(call.options.Env, "GOARCH") != selection.GOARCH ||
			environmentValue(call.options.Env, "CGO_ENABLED") != wantedCGO {
			t.Fatalf("selection %s did not receive its exact platform environment", selection.Name)
		}
		if slices.Contains(call.options.BuildFlags, "-tags=ambient") ||
			!slices.Contains(call.options.BuildFlags, "-gcflags=all=-N") {
			t.Fatalf("selection %s flags = %v", selection.Name, call.options.BuildFlags)
		}
	}
}

func TestConfiguredStyleExecutesPositiveBuildTags(t *testing.T) {
	root := writeStyleSelectionModule(t, map[string]string{
		"app/worker.go": "//go:build feature\n\npackage app\n\ntype Worker struct{}\n",
	})
	configuration := styleSelectionConfiguration(true)
	var flags [][]string
	service, err := New(Config{Loader: func(
		ctx context.Context,
		options load.Options,
		patterns ...string,
	) (*load.Program, error) {
		flags = append(flags, slices.Clone(options.BuildFlags))
		return load.Load(ctx, options, patterns...)
	}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Analyze(context.Background(), Request{
		WorkspaceRoot: root, Mode: AnalysisValidate,
		StyleConfiguration: &configuration,
	})
	if err != nil || !result.Diagnostics().Empty() {
		t.Fatalf("Analyze() = diagnostics %v, error %v", result.Diagnostics().Items(), err)
	}
	if len(flags) != 2 {
		t.Fatalf("loader flags = %v", flags)
	}
	for _, selectionFlags := range flags {
		if !slices.Contains(selectionFlags, "-tags=feature") {
			t.Fatalf("selection flags = %v", selectionFlags)
		}
	}
}

func TestConfiguredGenerationPreservesCompilerOwnedAnalysisTag(t *testing.T) {
	root := writeStyleSelectionModule(t, map[string]string{
		"app/worker.go": "package app\n\ntype Worker struct{}\n",
	})
	falseValue := false
	selection := compilerstyle.BuildSelection{
		Name: "linux-amd64-feature", SourceRoots: []string{"app"},
		GOOS: "linux", GOARCH: "amd64", CGOEnabled: &falseValue,
		Tags: []string{"feature"},
	}
	service, err := New(Config{LoadOptions: load.Options{
		Env:        []string{"GOFLAGS=-tags=ambient", "GOOS=plan9", "GOARCH=386", "CGO_ENABLED=1"},
		BuildFlags: []string{"-tags=ambient", "-gcflags=all=-N"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	options := service.analysisLoadOptions(normalizedRequest{
		root: root, mode: AnalysisGenerate, selection: &selection,
	})
	if !slices.Contains(options.BuildFlags, "-tags=feature,spice_generate") ||
		slices.Contains(options.BuildFlags, "-tags=ambient") ||
		!slices.Contains(options.BuildFlags, "-gcflags=all=-N") {
		t.Fatalf("generation build flags = %v", options.BuildFlags)
	}
	if environmentValue(options.Env, "GOFLAGS") != "" ||
		environmentValue(options.Env, "GOOS") != "linux" ||
		environmentValue(options.Env, "GOARCH") != "amd64" ||
		environmentValue(options.Env, "CGO_ENABLED") != "0" {
		t.Fatal("generation environment did not retain the exact configured selection")
	}
}

func TestConfiguredStyleReportsUnreachableHandwrittenSource(t *testing.T) {
	root := writeStyleSelectionModule(t, map[string]string{
		"app/worker_plan9.go": "package app\n\ntype Plan9Worker struct{}\n",
	})
	configuration := styleSelectionConfiguration(false)
	service, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Analyze(context.Background(), Request{
		WorkspaceRoot:      root,
		Mode:               AnalysisValidate,
		StyleConfiguration: &configuration,
	})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	items := result.Diagnostics().Items()
	if len(items) == 0 || items[0].Code != "spice.style.configuration.source-selection" ||
		!strings.Contains(items[0].Message, "unreachable") ||
		len(items[0].Related) != 1 ||
		!strings.Contains(items[0].Related[0].Message, "linux-amd64-default, windows-amd64-default") {
		t.Fatalf("unreachable diagnostics = %#v", items)
	}
}

func TestConfiguredStyleReportsMissingSourceRoot(t *testing.T) {
	root := writeStyleSelectionModule(t, map[string]string{
		"app/worker.go": "package app\n\ntype Worker struct{}\n",
	})
	configuration := styleSelectionConfiguration(false)
	configuration.SourceRoots = []string{"missing"}
	configuration.GeneratedRoots = []string{"missing/internal/spicegen"}
	for index := range configuration.BuildSelections {
		configuration.BuildSelections[index].SourceRoots = []string{"missing"}
	}
	service, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Analyze(context.Background(), Request{
		WorkspaceRoot:      root,
		Mode:               AnalysisValidate,
		StyleConfiguration: &configuration,
	})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	found := false
	for _, item := range result.Diagnostics().Items() {
		if item.Code == "spice.style.configuration.source-selection" &&
			strings.Contains(item.Message, "cannot be inspected") {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing-root diagnostics = %#v", result.Diagnostics().Items())
	}
}

func TestSelectedHandwrittenSourcesAcceptsCanonicalWorkspaceAlias(t *testing.T) {
	physicalRoot := writeStyleSelectionModule(t, map[string]string{
		"app/worker.go": "package app\n\ntype Worker struct{}\n",
	})
	aliasRoot := filepath.Join(t.TempDir(), "workspace-alias")
	if err := os.Symlink(physicalRoot, aliasRoot); err != nil {
		t.Skipf("workspace alias creation is unavailable: %v", err)
	}
	configuration := styleSelectionConfiguration(false)
	selected, diagnostics, err := selectedHandwrittenSources(aliasRoot, configuration)
	if err != nil {
		t.Fatalf("selectedHandwrittenSources() error = %v", err)
	}
	if len(diagnostics) != 0 {
		t.Fatalf("selectedHandwrittenSources() diagnostics = %#v", diagnostics)
	}
	want := filepath.Join(aliasRoot, "app", "worker.go")
	if !slices.Equal(selected, []string{want}) {
		t.Fatalf("selectedHandwrittenSources() = %v, want [%s]", selected, want)
	}
}

func TestSelectedHandwrittenSourcesRejectsTerminalSourceRootLink(t *testing.T) {
	root := writeStyleSelectionModule(t, map[string]string{
		"physical/worker.go": "package physical\n\ntype Worker struct{}\n",
	})
	linked := filepath.Join(root, "linked")
	if err := os.Symlink(filepath.Join(root, "physical"), linked); err != nil {
		t.Skipf("source-root link creation is unavailable: %v", err)
	}
	configuration := styleSelectionConfiguration(false)
	configuration.SourceRoots = []string{"linked"}
	selected, diagnostics, err := selectedHandwrittenSources(root, configuration)
	if err != nil {
		t.Fatalf("selectedHandwrittenSources() error = %v", err)
	}
	if len(selected) != 0 || len(diagnostics) != 1 ||
		diagnostics[0].Code != "spice.style.configuration.source-selection" ||
		!strings.Contains(diagnostics[0].Message, "must not be a symbolic link") {
		t.Fatalf("selected = %v, diagnostics = %#v", selected, diagnostics)
	}
}

func TestConfiguredStyleDeduplicatesPhysicalDiagnosticsWithSelectionIDs(t *testing.T) {
	root := writeStyleSelectionModule(t, map[string]string{
		"app/broken.go": "package app\n\nfunc (\n",
	})
	configuration := styleSelectionConfiguration(false)
	service, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Analyze(context.Background(), Request{
		WorkspaceRoot:      root,
		Mode:               AnalysisValidate,
		StyleConfiguration: &configuration,
	})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	found := false
	for _, item := range result.Diagnostics().Items() {
		if strings.HasPrefix(item.Code, "spice.load.") && len(item.Related) == 1 &&
			strings.Contains(item.Related[0].Message, "linux-amd64-default, windows-amd64-default") {
			found = true
		}
	}
	if !found {
		t.Fatalf("deduplicated diagnostics = %#v", result.Diagnostics().Items())
	}
}

func TestMergeSelectionDiagnosticsPreservesOriginalRelatedInformation(t *testing.T) {
	location := diagnostic.SourceLocation(".", "app/worker.go", "app/worker.go", 3, 1, 10)
	relatedOne := diagnostic.RelatedInformation{
		Message:  "first compiler relationship",
		Location: diagnostic.SourceLocation(".", "app/first.go", "app/first.go", 1, 1, 0),
	}
	relatedTwo := diagnostic.RelatedInformation{
		Message:  "second compiler relationship",
		Location: diagnostic.SourceLocation(".", "app/second.go", "app/second.go", 1, 1, 0),
	}
	base := diagnostic.New(
		"spice.style.file.name",
		diagnostic.SeverityError,
		"primary type must match its file name",
		location,
	)
	merged := mergeSelectionDiagnostics([]selectionResult{
		{id: "linux-amd64-default", result: Result{diagnostics: diagnostic.NewSet(base.WithRelated(relatedOne))}},
		{id: "windows-amd64-default", result: Result{diagnostics: diagnostic.NewSet(base.WithRelated(relatedTwo))}},
	}).Items()
	if len(merged) != 1 || len(merged[0].Related) != 3 {
		t.Fatalf("merged diagnostics = %#v", merged)
	}
	messages := make([]string, len(merged[0].Related))
	for index, item := range merged[0].Related {
		messages[index] = item.Message
	}
	for _, wanted := range []string{
		"first compiler relationship",
		"second compiler relationship",
		"build selections: linux-amd64-default, windows-amd64-default",
	} {
		if !slices.Contains(messages, wanted) {
			t.Fatalf("related messages = %v, want %q", messages, wanted)
		}
	}
}

func TestConfiguredStyleHonorsCancellationBeforeSelectionLoading(t *testing.T) {
	root := writeStyleSelectionModule(t, map[string]string{
		"app/worker.go": "package app\n\ntype Worker struct{}\n",
	})
	configuration := styleSelectionConfiguration(false)
	service, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Analyze(ctx, Request{
		WorkspaceRoot:      root,
		Mode:               AnalysisValidate,
		StyleConfiguration: &configuration,
	}); err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("Analyze() cancellation error = %v", err)
	}
}

func TestConfiguredStyleStopsAfterCancellationDuringSelectionLoading(t *testing.T) {
	root := writeStyleSelectionModule(t, map[string]string{
		"app/worker.go": "package app\n\ntype Worker struct{}\n",
	})
	configuration := styleSelectionConfiguration(false)
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	service, err := New(Config{Loader: func(
		loadContext context.Context,
		_ load.Options,
		_ ...string,
	) (*load.Program, error) {
		calls++
		cancel()
		return nil, loadContext.Err()
	}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Analyze(ctx, Request{
		WorkspaceRoot:      root,
		Mode:               AnalysisValidate,
		StyleConfiguration: &configuration,
	})
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("Analyze() cancellation error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("loader calls = %d, want 1", calls)
	}
}

func styleSelectionConfiguration(tagged bool) compilerstyle.Configuration {
	disabled := compilerstyle.RuleLevelOff
	falseValue := false
	tags := []string(nil)
	if tagged {
		tags = []string{"feature"}
	}
	return compilerstyle.Configuration{
		SchemaVersion: 2,
		Profile:       string(compilerstyle.ProfileJavaStructured),
		SourceRoots:   []string{"app"},
		GeneratedRoots: []string{
			"app/internal/spicegen",
		},
		BuildSelections: []compilerstyle.BuildSelection{
			{
				Name: "linux-amd64-default", SourceRoots: []string{"app"},
				GOOS: "linux", GOARCH: "amd64", CGOEnabled: &falseValue, Tags: tags,
			},
			{
				Name: "windows-amd64-default", SourceRoots: []string{"app"},
				GOOS: "windows", GOARCH: "amd64", CGOEnabled: &falseValue, Tags: tags,
			},
		},
		Rules: compilerstyle.Rules{
			OnePrimaryTypePerFile: disabled, MethodsInPrimaryFile: disabled,
			FileNameMatchesType: disabled, PackageFunctions: disabled,
			ExplicitConstructors: disabled, ExplicitManagedScopes: disabled,
			BanInit: disabled, BanMutablePackageState: disabled,
			PrivateManagedFields: disabled, ModuleOwnership: disabled,
			RouteClassification: disabled, ContextFirst: disabled,
			ErrorLast: disabled, MaxTypeFileLines: 500,
		},
	}
}

func writeStyleSelectionModule(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	files["go.mod"] = "module example.com/style-selection\n\ngo 1.26.0\n"
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func environmentValue(environment []string, name string) string {
	for _, entry := range environment {
		key, value, found := strings.Cut(entry, "=")
		if found && strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}

func FuzzExactStyleSelectionOptionsScrubsAmbientBuildState(f *testing.F) {
	f.Add("GOFLAGS=-tags=ambient", "-tags=ambient")
	f.Add("GOOS=plan9", "-tags=ambient")
	f.Fuzz(func(t *testing.T, environmentEntry, buildFlag string) {
		falseValue := false
		selection := compilerstyle.BuildSelection{
			Name: "linux-amd64-feature", SourceRoots: []string{"app"},
			GOOS: "linux", GOARCH: "amd64", CGOEnabled: &falseValue,
			Tags: []string{"feature"},
		}
		first := exactStyleSelectionOptions(load.Options{
			Env:        []string{"PATH=test", environmentEntry},
			BuildFlags: []string{buildFlag},
		}, selection)
		second := exactStyleSelectionOptions(load.Options{
			Env:        []string{"PATH=test", environmentEntry},
			BuildFlags: []string{buildFlag},
		}, selection)
		if !slices.Equal(first.Env, second.Env) || !slices.Equal(first.BuildFlags, second.BuildFlags) {
			t.Fatal("selection option normalization is nondeterministic")
		}
		if environmentValue(first.Env, "GOOS") != "linux" ||
			environmentValue(first.Env, "GOARCH") != "amd64" ||
			environmentValue(first.Env, "CGO_ENABLED") != "0" ||
			environmentValue(first.Env, "GOFLAGS") != "" {
			t.Fatalf("normalized environment = %v", first.Env)
		}
		if !slices.Contains(first.BuildFlags, "-tags=feature") ||
			slices.Contains(first.BuildFlags, "-tags=ambient") {
			t.Fatalf("normalized build flags = %v", first.BuildFlags)
		}
	})
}

func BenchmarkMergeStyleSelectionDiagnostics(b *testing.B) {
	items := make([]diagnostic.Diagnostic, 100)
	for index := range items {
		path := filepath.Join("app", "file"+strconv.Itoa(index)+".go")
		items[index] = diagnostic.New(
			"spice.style.file.name",
			diagnostic.SeverityError,
			"primary type must match its file name",
			diagnostic.SourceLocation(".", path, path, index+1, 1, index),
		)
	}
	results := make([]selectionResult, 4)
	for index := range results {
		results[index] = selectionResult{
			id: "selection-" + strconv.Itoa(index),
			result: Result{
				diagnostics: diagnostic.NewSet(items...),
			},
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if merged := mergeSelectionDiagnostics(results); len(merged.Items()) != len(items) {
			b.Fatalf("merged diagnostics = %d, want %d", len(merged.Items()), len(items))
		}
	}
}
