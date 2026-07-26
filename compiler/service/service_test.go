package service

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/StevenBuglione/spice/annotation"
	"github.com/StevenBuglione/spice/compiler/load"
	compilerstarter "github.com/StevenBuglione/spice/compiler/starter"
	publicstarter "github.com/StevenBuglione/spice/starter"
)

func TestServiceAnalyzesOverlayWithoutFilesystemWrites(t *testing.T) {
	t.Parallel()
	root := writeServiceModule(t)
	mainPath := filepath.Join(root, "main.go")
	original, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatalf("ReadFile(main.go) error = %v", err)
	}
	service, err := New(Config{})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	invalid := strings.Replace(
		string(original),
		"// @Application",
		"// @Application\n// @Unknown",
		1,
	)
	invalidResult, err := service.Analyze(
		context.Background(),
		Request{
			WorkspaceRoot: root,
			Overlay: map[string]Document{
				mainPath: {Version: 7, Content: []byte(invalid)},
			},
		},
	)
	if err != nil {
		t.Fatalf("Analyze(invalid) error = %v", err)
	}
	if invalidResult.Diagnostics().Empty() {
		t.Fatal("Analyze(invalid) diagnostics are empty")
	}
	if invalidResult.GenerationReady() {
		t.Fatal("Analyze(invalid) GenerationReady() = true, want false")
	}
	diagnostic := invalidResult.Diagnostics().Items()[0]
	if !strings.Contains(diagnostic.Message, "unknown annotation") ||
		diagnostic.Location.Path != filepath.ToSlash(mainPath) {
		t.Fatalf("invalid diagnostic = %+v", diagnostic)
	}

	uriPath := filepath.ToSlash(mainPath)
	if runtime.GOOS == "windows" && !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	mainURI := (&url.URL{
		Scheme: "file",
		Path:   uriPath,
	}).String()
	result, err := service.Analyze(
		context.Background(),
		Request{
			WorkspaceRoot: root,
			Overlay: map[string]Document{
				mainURI: {Version: 8, Content: original},
			},
		},
	)
	if err != nil {
		t.Fatalf("Analyze(valid) error = %v", err)
	}
	if !result.Diagnostics().Empty() {
		t.Fatalf("Analyze(valid) diagnostics = %+v", result.Diagnostics().Items())
	}
	if !result.GenerationReady() {
		t.Fatal("Analyze(valid) GenerationReady() = false, want true")
	}
	plan, found := result.GenerationPlan()
	if !found || plan.Target().PackagePath != "example.com/servicefixture" {
		t.Fatalf("GenerationPlan() = %+v, %t", plan.Target(), found)
	}
	if len(plan.Files()) == 0 {
		t.Fatal("GenerationPlan() files are empty")
	}
	if len(result.ApplicationModel().Targets()) != 1 {
		t.Fatalf(
			"ApplicationModel().Targets() = %d, want 1",
			len(result.ApplicationModel().Targets()),
		)
	}
	if len(result.Annotations()) < 3 {
		t.Fatalf("Annotations() = %d, want at least 3", len(result.Annotations()))
	}
	if len(result.ProviderGraph().Providers) != 1 {
		t.Fatalf(
			"ProviderGraph().Providers = %d, want 1",
			len(result.ProviderGraph().Providers),
		)
	}
	if len(result.ModuleGraph().Modules) != 1 {
		t.Fatalf(
			"ModuleGraph().Modules = %d, want 1",
			len(result.ModuleGraph().Modules),
		)
	}
	configurations := result.Configurations()
	if len(configurations) != 1 ||
		len(configurations[0].Fields) != 1 ||
		configurations[0].Fields[0].Key != "orders.limit" {
		t.Fatalf("Configurations() = %+v", configurations)
	}
	if len(result.AnnotationDefinitions()) == 0 {
		t.Fatal("AnnotationDefinitions() are empty")
	}
	for _, relativePath := range []string{
		"zz_spice_gen.go",
		".spice/servicefixture.manifest.json",
	} {
		if _, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(relativePath))); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("overlay analysis wrote %s: %v", relativePath, statErr)
		}
	}
}

func TestServiceCacheIsBoundedAndResultsAreDefensive(t *testing.T) {
	t.Parallel()
	root := writeServiceModule(t)
	var loads atomic.Int64
	service, err := New(Config{
		MaxCacheEntries: 1,
		Loader: func(
			ctx context.Context,
			options load.Options,
			patterns ...string,
		) (*load.Program, error) {
			loads.Add(1)
			return load.Load(ctx, options, patterns...)
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	request := Request{
		WorkspaceRoot: root,
		ContentHash:   "snapshot-1",
	}
	first, err := service.Analyze(context.Background(), request)
	if err != nil {
		t.Fatalf("Analyze(first) error = %v", err)
	}
	annotations := first.Annotations()
	annotations[0].Name = "mutated"
	definitions := first.AnnotationDefinitions()
	definitions[0].Arguments = nil
	configurations := first.Configurations()
	configurations[0].Fields[0].Key = "mutated"

	second, err := service.Analyze(context.Background(), request)
	if err != nil {
		t.Fatalf("Analyze(second) error = %v", err)
	}
	if loads.Load() != 1 {
		t.Fatalf("loader calls = %d after cache hit, want 1", loads.Load())
	}
	if second.Annotations()[0].Name == "mutated" ||
		second.Configurations()[0].Fields[0].Key == "mutated" {
		t.Fatal("cached result was mutated through a defensive getter")
	}

	mainPath := filepath.Join(root, "main.go")
	original, readErr := os.ReadFile(mainPath)
	if readErr != nil {
		t.Fatalf("ReadFile(main.go) error = %v", readErr)
	}
	changed := Request{
		WorkspaceRoot: root,
		ContentHash:   "snapshot-2",
		Overlay: map[string]Document{
			mainPath: {
				Version: 1,
				Content: append(original, '\n'),
			},
		},
	}
	if _, err := service.Analyze(context.Background(), changed); err != nil {
		t.Fatalf("Analyze(changed) error = %v", err)
	}
	if _, err := service.Analyze(context.Background(), request); err != nil {
		t.Fatalf("Analyze(evicted) error = %v", err)
	}
	if loads.Load() != 3 {
		t.Fatalf("loader calls after eviction = %d, want 3", loads.Load())
	}
}

func TestServiceRejectsStaleSequencedAnalysis(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	firstStarted := make(chan struct{})
	var calls atomic.Int64
	service, err := New(Config{
		Loader: func(
			ctx context.Context,
			_ load.Options,
			_ ...string,
		) (*load.Program, error) {
			if calls.Add(1) == 1 {
				close(firstStarted)
				<-ctx.Done()
				return nil, ctx.Err()
			}
			return nil, errors.New("synthetic load failure")
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	firstDone := make(chan error, 1)
	go func() {
		_, analyzeErr := service.Analyze(
			context.Background(),
			Request{WorkspaceRoot: root, Sequence: 1},
		)
		firstDone <- analyzeErr
	}()
	<-firstStarted
	second, err := service.Analyze(
		context.Background(),
		Request{WorkspaceRoot: root, Sequence: 2},
	)
	if err != nil {
		t.Fatalf("Analyze(second) error = %v", err)
	}
	if second.Diagnostics().Empty() {
		t.Fatal("Analyze(second) diagnostics are empty")
	}
	select {
	case firstErr := <-firstDone:
		if !errors.Is(firstErr, ErrStaleAnalysis) {
			t.Fatalf("Analyze(first) error = %v, want ErrStaleAnalysis", firstErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for stale analysis")
	}
	if _, err := service.Analyze(
		context.Background(),
		Request{WorkspaceRoot: root, Sequence: 1},
	); !errors.Is(err, ErrStaleAnalysis) {
		t.Fatalf("Analyze(old sequence) error = %v, want ErrStaleAnalysis", err)
	}
}

func TestServiceHonorsCallerCancellation(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	service, err := New(Config{
		Loader: func(
			ctx context.Context,
			_ load.Options,
			_ ...string,
		) (*load.Program, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Analyze(
		ctx,
		Request{WorkspaceRoot: root},
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("Analyze() error = %v, want context.Canceled", err)
	}
}

func TestServiceComposesSelectedStarterMetadata(t *testing.T) {
	t.Parallel()
	root := writeServiceModule(t)
	mainPath := filepath.Join(root, "main.go")
	content, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatalf("ReadFile(main.go) error = %v", err)
	}
	content = bytes.Replace(
		content,
		[]byte("// @Application"),
		[]byte("// @Application\n// @fixture.Enable"),
		1,
	)
	if writeErr := os.WriteFile(mainPath, content, 0o600); writeErr != nil {
		t.Fatalf("WriteFile(main.go) error = %v", writeErr)
	}
	starterPath := filepath.Join(root, "starter", "starter.go")
	if mkdirErr := os.MkdirAll(filepath.Dir(starterPath), 0o750); mkdirErr != nil {
		t.Fatalf("MkdirAll(starter) error = %v", mkdirErr)
	}
	if writeErr := os.WriteFile(
		starterPath,
		[]byte("package starter\n\ntype Client struct{}\n\nfunc New() Client { return Client{} }\n"),
		0o600,
	); writeErr != nil {
		t.Fatalf("WriteFile(starter.go) error = %v", writeErr)
	}
	manifest := publicstarter.Must(publicstarter.Spec{
		Schema:    publicstarter.Schema,
		ID:        "example.com/servicefixture/starter",
		Version:   "1.0.0",
		Module:    "example.com/servicefixture",
		SpiceAPI:  publicstarter.APIVersion,
		MinimumGo: "1.26",
		License:   "Apache-2.0",
		Review:    "docs/dependency-review.md",
		Activation: publicstarter.Activation{
			Mode: publicstarter.ActivationExplicitAnnotation,
			EntryPoints: []publicstarter.EntryPoint{{
				Package: "example.com/servicefixture/starter",
				Symbol:  "New",
			}},
		},
		Capabilities: []string{"fixture.client"},
		Dependencies: []publicstarter.Dependency{{
			Module:  "example.com/reviewed",
			Version: "v1.2.3",
			License: "MIT",
		}},
		Annotations: []publicstarter.AnnotationSpec{{
			Name:    "fixture.Enable",
			Targets: []annotation.Target{annotation.TargetFunction},
		}},
		ApplicationFeatures: []publicstarter.FeatureSpec{{
			Annotation: "fixture.Enable",
			Capability: "fixture.client",
			EntryPoints: []publicstarter.EntryPoint{{
				Package: "example.com/servicefixture/starter",
				Symbol:  "New",
			}},
		}},
	})
	catalog, err := compilerstarter.New(manifest)
	if err != nil {
		t.Fatalf("starter.New() error = %v", err)
	}
	var inspections atomic.Int64
	service, err := New(Config{
		StarterCatalog: catalog,
		ModuleVersions: func(
			_ context.Context,
			options load.Options,
		) ([]compilerstarter.ModuleVersion, error) {
			inspections.Add(1)
			if options.Dir != root {
				t.Fatalf("module inspection directory = %q, want %q", options.Dir, root)
			}
			return []compilerstarter.ModuleVersion{{
				Path:    "example.com/reviewed",
				Version: "v1.2.3",
			}}, nil
		},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	result, err := service.Analyze(
		context.Background(),
		Request{WorkspaceRoot: root},
	)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if !result.Diagnostics().Empty() || !result.GenerationReady() {
		t.Fatalf(
			"Analyze() diagnostics = %+v, ready = %t",
			result.Diagnostics().Items(),
			result.GenerationReady(),
		)
	}
	if result.TargetName() != "Servicefixture" {
		t.Fatalf("TargetName() = %q, want Servicefixture", result.TargetName())
	}
	if inspections.Load() != 1 {
		t.Fatalf("module inspections = %d, want 1", inspections.Load())
	}
	foundDefinition := false
	for _, definition := range result.AnnotationDefinitions() {
		if definition.Name == "fixture.Enable" {
			foundDefinition = true
		}
	}
	if !foundDefinition {
		t.Fatal("AnnotationDefinitions() omitted selected starter definition")
	}
	foundProvider := false
	for _, provider := range result.ProviderGraph().Providers {
		if provider.PackagePath == "example.com/servicefixture/starter" &&
			provider.Name == "New" {
			foundProvider = true
		}
	}
	if !foundProvider {
		t.Fatalf(
			"ProviderGraph() omitted selected starter constructor: %+v",
			result.ProviderGraph().Providers,
		)
	}

	misaligned, err := New(Config{
		StarterCatalog: catalog,
		ModuleVersions: func(
			context.Context,
			load.Options,
		) ([]compilerstarter.ModuleVersion, error) {
			return []compilerstarter.ModuleVersion{{
				Path:    "example.com/reviewed",
				Version: "v1.2.4",
			}}, nil
		},
	})
	if err != nil {
		t.Fatalf("New(misaligned) error = %v", err)
	}
	failed, err := misaligned.Analyze(
		context.Background(),
		Request{WorkspaceRoot: root},
	)
	if err != nil {
		t.Fatalf("Analyze(misaligned) error = %v", err)
	}
	items := failed.Diagnostics().Items()
	if len(items) != 1 ||
		items[0].Code != "spice.starter.version" ||
		failed.GenerationReady() {
		t.Fatalf(
			"Analyze(misaligned) diagnostics = %+v, ready = %t",
			items,
			failed.GenerationReady(),
		)
	}
}

func TestServiceRejectsUnsafeAndOversizedRequests(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	service, err := New(Config{
		MaxOverlayFiles: 1,
		MaxOverlayBytes: 4,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	tests := []Request{
		{},
		{
			WorkspaceRoot: root,
			Overlay: map[string]Document{
				"a.go": {},
				"b.go": {},
			},
		},
		{
			WorkspaceRoot: root,
			Overlay: map[string]Document{
				"a.go": {Content: []byte("12345")},
			},
		},
		{
			WorkspaceRoot: root,
			Overlay: map[string]Document{
				"../outside.go": {},
			},
		},
		{
			WorkspaceRoot: root,
			Overlay: map[string]Document{
				"https://example.com/main.go": {},
			},
		},
	}
	for _, request := range tests {
		if _, err := service.Analyze(
			context.Background(),
			request,
		); err == nil {
			t.Fatalf("Analyze(%+v) error = nil, want failure", request)
		}
	}
	for _, config := range []Config{
		{MaxCacheEntries: -1},
		{MaxOverlayFiles: -1},
		{MaxOverlayBytes: -1},
	} {
		if _, err := New(config); err == nil {
			t.Fatalf("New(%+v) error = nil, want failure", config)
		}
	}
}

func BenchmarkServiceCachedOverlayAnalysis(b *testing.B) {
	root := writeServiceModule(b)
	service, err := New(Config{})
	if err != nil {
		b.Fatalf("New() error = %v", err)
	}
	mainPath := filepath.Join(root, "main.go")
	content, err := os.ReadFile(mainPath)
	if err != nil {
		b.Fatalf("ReadFile(main.go) error = %v", err)
	}
	request := Request{
		WorkspaceRoot: root,
		ContentHash:   "benchmark-snapshot",
		Overlay: map[string]Document{
			mainPath: {Version: 1, Content: content},
		},
	}
	if _, err := service.Analyze(context.Background(), request); err != nil {
		b.Fatalf("Analyze(warm) error = %v", err)
	}
	b.ResetTimer()
	for range b.N {
		if _, err := service.Analyze(context.Background(), request); err != nil {
			b.Fatalf("Analyze() error = %v", err)
		}
	}
}

func FuzzNormalizeOverlay(f *testing.F) {
	root := f.TempDir()
	f.Add("main.go", []byte("package main\n"), 1)
	f.Add("../outside.go", []byte("package outside\n"), 2)
	f.Add("file:///tmp/main.go", []byte("package main\n"), 3)
	f.Fuzz(func(
		t *testing.T,
		identity string,
		content []byte,
		version int,
	) {
		if len(content) > 1_024 {
			content = content[:1_024]
		}
		result, err := normalizeOverlay(
			root,
			map[string]Document{
				identity: {Version: version, Content: content},
			},
			1,
			1_024,
		)
		if err != nil {
			return
		}
		if len(result) != 1 {
			t.Fatalf("normalizeOverlay() length = %d, want 1", len(result))
		}
		for filePath, document := range result {
			relative, relativeErr := filepath.Rel(root, filePath)
			if relativeErr != nil ||
				(relative != "." && !filepath.IsLocal(relative)) {
				t.Fatalf(
					"normalized overlay escaped root: path=%q err=%v",
					filePath,
					relativeErr,
				)
			}
			if document.Version != version ||
				!bytes.Equal(document.Content, content) {
				t.Fatalf("normalized document = %+v", document)
			}
		}
	})
}

type testingTB interface {
	Helper()
	TempDir() string
	Fatalf(string, ...any)
}

func writeServiceModule(tb testingTB) string {
	tb.Helper()
	root := tb.TempDir()
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		tb.Fatalf("Abs(repository) error = %v", err)
	}
	files := map[string]string{
		"go.mod": "module example.com/servicefixture\n\ngo 1.26.0\n\n" +
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
		"orders/doc.go": `// Package orders owns order configuration.
// @Module
package orders
`,
		"orders/config.go": `package orders

// @Configuration(prefix="orders")
type Settings struct {
	Limit int ` + "`spice:\"limit,default=100\"`" + `
}
`,
	}
	for relativePath, content := range files {
		filePath := filepath.Join(root, filepath.FromSlash(relativePath))
		if err := os.MkdirAll(filepath.Dir(filePath), 0o750); err != nil {
			tb.Fatalf("MkdirAll(%s) error = %v", relativePath, err)
		}
		if err := os.WriteFile(filePath, []byte(content), 0o600); err != nil {
			tb.Fatalf("WriteFile(%s) error = %v", relativePath, err)
		}
	}
	return root
}
