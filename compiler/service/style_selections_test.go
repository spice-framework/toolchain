package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
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
		"app/worker_linux.go":   "package app\n\ntype Worker struct{}\n",
		"app/worker_windows.go": "package app\n\ntype Worker struct{}\n",
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

func TestConfiguredStyleScopesGeneratedApplicationsIndependently(t *testing.T) {
	root := writeServiceModule(t)
	for _, relative := range []string{
		"main.go",
		"orders/doc.go",
		"orders/config.go",
		"internal/spicegen/servicefixture/spice_command_gen.go",
	} {
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(relative))); err != nil {
			t.Fatal(err)
		}
	}
	for _, target := range []string{"one", "two"} {
		writeServiceFixtureFile(t, root, "cmd/"+target+"/application.go", fmt.Sprintf(`//go:build spice_generate

package main

import (
	"os"

	_ "example.com/servicefixture/%[1]s"
	spiceapp "example.com/servicefixture/internal/spicegen/%[1]s"
)

// @import { Application } from "github.com/spice-framework/spice/annotation/core"

// @Application
func main() {
	os.Exit(spiceapp.Main(os.Args[1:]))
}
`, target))
		writeServiceFixtureFile(t, root, target+"/settings.go", `package `+target+`

// @import { ConfigurationProperties } from "github.com/spice-framework/spice/annotation/core"

// @ConfigurationProperties(prefix="agent")
type Settings struct {
	Workspace string `+"`spice:\"workspace,env=SPICE_AGENT_WORKSPACE\"`"+`
}
`)
	}
	falseValue := false
	disabled := compilerstyle.RuleLevelOff
	configuration := compilerstyle.Configuration{
		SchemaVersion: 2,
		Profile:       string(compilerstyle.ProfileJavaStructured),
		SourceRoots:   []string{"cmd", "one", "two"},
		GeneratedRoots: []string{
			"cmd/internal/spicegen",
		},
		BuildSelections: []compilerstyle.BuildSelection{{
			Name: "linux-amd64-default", SourceRoots: []string{"cmd", "one", "two"},
			GOOS: "linux", GOARCH: "amd64", CGOEnabled: &falseValue,
		}},
		Rules: compilerstyle.Rules{
			OnePrimaryTypePerFile: disabled, MethodsInPrimaryFile: disabled,
			FileNameMatchesType: disabled, PackageFunctions: disabled,
			ExplicitConstructors: disabled, ExplicitManagedScopes: disabled,
			BanInit: disabled, BanMutablePackageState: disabled,
			PrivateManagedFields: disabled, ModuleOwnership: disabled,
			RouteClassification: disabled, ContextFirst: disabled,
			ErrorLast: disabled, MaxTypeFileLines: 500,
		},
		AllowedBoundaryFiles: []string{"cmd/*/application.go"},
		PackageFunctionExceptions: []compilerstyle.PackageFunctionException{{
			Glob: "cmd/*/application.go", ContributionKind: "application",
			Maximum: 1, Reason: "compiler-validated generated application bridge",
		}},
	}
	service, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceCleanup(t, service)
	result, err := service.Analyze(t.Context(), Request{
		WorkspaceRoot: root, Mode: AnalysisValidate,
		StyleConfiguration: &configuration,
	})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if diagnostics := result.Diagnostics().Items(); len(diagnostics) != 0 {
		t.Fatalf("Analyze() diagnostics = %#v", diagnostics)
	}
	wantScopes := []ApplicationScope{
		{BuildSelection: "linux-amd64-default", PackagePath: "example.com/servicefixture/cmd/one", GeneratedEntrypoint: true},
		{BuildSelection: "linux-amd64-default", PackagePath: "example.com/servicefixture/cmd/two", GeneratedEntrypoint: true},
	}
	if !reflect.DeepEqual(result.ApplicationScopes(), wantScopes) {
		t.Fatalf("ApplicationScopes() = %#v, want %#v", result.ApplicationScopes(), wantScopes)
	}
	mutated := result.ApplicationScopes()
	mutated[0].PackagePath = "mutated"
	if result.ApplicationScopes()[0].PackagePath == "mutated" {
		t.Fatal("ApplicationScopes() returned mutable storage")
	}
	configurations := result.Configurations()
	if len(configurations) != 2 || configurations[0].Prefix != "agent" ||
		configurations[1].Prefix != "agent" {
		t.Fatalf("Configurations() = %#v", configurations)
	}
}

func TestConfiguredStyleStillRejectsDuplicateConfigurationWithinOneApplication(t *testing.T) {
	root, configuration := writeIndependentGeneratedApplications(t)
	writeServiceFixtureFile(t, root, "one/other_settings.go", "package one\n\n"+
		"// @import { ConfigurationProperties } from \"github.com/spice-framework/spice/annotation/core\"\n\n"+
		"// @ConfigurationProperties(prefix=\"agent\")\n"+
		"type OtherSettings struct {\n"+
		"\tWorkspace string `spice:\"workspace,env=SPICE_AGENT_WORKSPACE\"`\n"+
		"}\n")
	service, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceCleanup(t, service)
	result, err := service.Analyze(t.Context(), Request{
		WorkspaceRoot: root, Mode: AnalysisValidate,
		StyleConfiguration: &configuration,
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range result.Diagnostics().Items() {
		if strings.Contains(item.Message, "agent.workspace") &&
			strings.Contains(item.Message, "duplicate") &&
			slices.ContainsFunc(item.Related, func(related diagnostic.RelatedInformation) bool {
				return strings.Contains(related.Message, "example.com/servicefixture/cmd/one")
			}) {
			found = true
		}
	}
	if !found {
		t.Fatalf("duplicate diagnostics = %#v", result.Diagnostics().Items())
	}
}

func TestConfiguredStyleReportsApplicationSemanticOrphan(t *testing.T) {
	root, configuration := writeIndependentGeneratedApplications(t)
	writeServiceFixtureFile(t, root, "orphan/settings.go", "package orphan\n\n"+
		"// @import { ConfigurationProperties } from \"github.com/spice-framework/spice/annotation/core\"\n\n"+
		"// @ConfigurationProperties(prefix=\"orphan\")\n"+
		"type Settings struct {\n\tValue string `spice:\"value\"`\n}\n")
	configuration.SourceRoots = []string{"cmd", "one", "orphan", "two"}
	configuration.BuildSelections[0].SourceRoots = slices.Clone(configuration.SourceRoots)
	service, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceCleanup(t, service)
	result, err := service.Analyze(t.Context(), Request{
		WorkspaceRoot: root, Mode: AnalysisValidate,
		StyleConfiguration: &configuration,
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range result.Diagnostics().Items() {
		if item.Code == "spice.style.configuration.application-selection" &&
			strings.Contains(item.Message, "example.com/servicefixture/orphan") {
			found = true
		}
	}
	if !found {
		t.Fatalf("semantic coverage diagnostics = %#v", result.Diagnostics().Items())
	}
}

func TestConfiguredStylePromotesLocalModuleDependenciesPerApplication(t *testing.T) {
	root, configuration := writeIndependentGeneratedApplications(t)
	writeServiceFixtureFile(t, root, "one/doc.go", `// @import { Module } from "github.com/spice-framework/spice/annotation/modulith"

// Package one owns the first application.
// @Module(allowedDependencies=["example.com/servicefixture/processplatform", "example.com/servicefixture/runidentity"])
package one
`)
	writeServiceFixtureFile(t, root, "one/architecture.go", `package one

import (
	"example.com/servicefixture/processplatform"
	"example.com/servicefixture/runidentity"
)

type Architecture struct {
	Process processplatform.Service
	Run runidentity.Service
}
`)
	for _, dependency := range []string{"processplatform", "runidentity"} {
		writeServiceFixtureFile(t, root, dependency+"/doc.go", `// @import { Module } from "github.com/spice-framework/spice/annotation/modulith"

// Package `+dependency+` owns one local dependency.
// @Module
package `+dependency+`
`)
		writeServiceFixtureFile(t, root, dependency+"/service.go", "package "+dependency+"\n\ntype Service struct{}\n")
	}
	configuration.SourceRoots = []string{"cmd", "one", "processplatform", "runidentity", "two"}
	configuration.BuildSelections[0].SourceRoots = slices.Clone(configuration.SourceRoots)
	configuration.AllowedBoundaryFiles = []string{
		"cmd/*/application.go",
		"one/doc.go",
		"processplatform/doc.go",
		"runidentity/doc.go",
	}
	service, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceCleanup(t, service)
	result, err := service.Analyze(t.Context(), Request{
		WorkspaceRoot: root, Mode: AnalysisValidate,
		StyleConfiguration: &configuration,
	})
	if err != nil {
		t.Fatal(err)
	}
	if diagnostics := result.Diagnostics().Items(); len(diagnostics) != 0 {
		t.Fatalf("module dependency diagnostics = %#v", diagnostics)
	}
}

func TestConfiguredStyleReloadsCollapsedRootDependenciesSyntaxComplete(t *testing.T) {
	root := writeServiceModule(t)
	for _, relative := range []string{"main.go", "orders"} {
		if err := os.RemoveAll(filepath.Join(root, filepath.FromSlash(relative))); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.RemoveAll(filepath.Join(root, "internal", "spicegen")); err != nil {
		t.Fatal(err)
	}
	writeServiceFixtureFile(t, root, "cmd/app/application.go", `//go:build spice_generate

// @import { Module } from "github.com/spice-framework/spice/annotation/modulith"
// @Module(allowedDependencies=["example.com/servicefixture/internal/app"])
package main

import (
	"os"

	_ "example.com/servicefixture/internal/app"
	spiceapp "example.com/servicefixture/internal/spicegen/app"
)

// @import { Application } from "github.com/spice-framework/spice/annotation/core"

// @Application
func main() {
	os.Exit(spiceapp.Main(os.Args[1:]))
}
`)
	writeServiceFixtureFile(t, root, "internal/app/doc.go", `// @import { Module } from "github.com/spice-framework/spice/annotation/modulith"

// Package app owns the collapsed-root application composition.
// @Module(allowedDependencies=["example.com/servicefixture/internal/processplatform", "example.com/servicefixture/internal/runidentity"])
package app
`)
	writeServiceFixtureFile(t, root, "internal/app/architecture.go", `package app

import (
	"example.com/servicefixture/internal/processplatform"
	"example.com/servicefixture/internal/runidentity"
)

type Architecture struct {
	Process processplatform.Service
	Run runidentity.Service
}
`)
	for _, dependency := range []string{"processplatform", "runidentity"} {
		writeServiceFixtureFile(t, root, "internal/"+dependency+"/doc.go", `// @import { Module } from "github.com/spice-framework/spice/annotation/modulith"

// Package `+dependency+` owns one local dependency.
// @Module
package `+dependency+`
`)
		writeServiceFixtureFile(t, root, "internal/"+dependency+"/service.go", "package "+dependency+"\n\ntype Service struct{}\n")
	}
	configuration := collapsedModuleConfiguration([]string{"cmd", "internal"})
	configuration.AllowedBoundaryFiles = []string{
		"cmd/app/application.go",
		"internal/app/doc.go",
		"internal/processplatform/doc.go",
		"internal/runidentity/doc.go",
	}
	configuration.PackageFunctionExceptions = []compilerstyle.PackageFunctionException{{
		Glob: "cmd/app/application.go", ContributionKind: "application",
		Maximum: 1, Reason: "compiler-validated generated application bridge",
	}}
	service, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceCleanup(t, service)
	result, err := service.Analyze(t.Context(), Request{
		WorkspaceRoot: root, Mode: AnalysisValidate,
		StyleConfiguration: &configuration,
	})
	if err != nil {
		t.Fatal(err)
	}
	if diagnostics := result.Diagnostics().Items(); len(diagnostics) != 0 {
		t.Fatalf("collapsed-root dependency diagnostics = %#v", diagnostics)
	}
	if got := len(result.ModuleGraph().Modules); got != 4 {
		t.Fatalf("collapsed-root module count = %d, want 4", got)
	}
}

func TestConfiguredStyleUnionsPlatformModuleIdentities(t *testing.T) {
	root, configuration := writePlatformModuleUniverseFixture(t)
	orders := []compilerstyle.Configuration{configuration.Clone(), configuration.Clone()}
	for index := range orders {
		service, err := New(Config{})
		if err != nil {
			t.Fatal(err)
		}
		result, analyzeErr := service.Analyze(t.Context(), Request{
			WorkspaceRoot: root, Mode: AnalysisValidate,
			StyleConfiguration: &orders[index],
		})
		closeErr := service.Close(t.Context())
		if analyzeErr != nil {
			t.Fatal(analyzeErr)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		if diagnostics := result.Diagnostics().Items(); len(diagnostics) != 0 {
			t.Fatalf("selection order %d diagnostics = %#v", index, diagnostics)
		}
	}

	writePlatformApplicationModule(t, root, "example.com/servicefixture/internal/missing::spi")
	var previousMessages []string
	for index := range orders {
		service, err := New(Config{})
		if err != nil {
			t.Fatal(err)
		}
		result, analyzeErr := service.Analyze(t.Context(), Request{
			WorkspaceRoot: root, Mode: AnalysisValidate,
			StyleConfiguration: &orders[index],
		})
		closeErr := service.Close(t.Context())
		if analyzeErr != nil {
			t.Fatal(analyzeErr)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		var selected []string
		for _, item := range result.Diagnostics().Items() {
			if strings.Contains(item.Message, "allows unknown module example.com/servicefixture/internal/missing") {
				selected = append(selected, item.Message)
				if !slices.ContainsFunc(item.Related, func(related diagnostic.RelatedInformation) bool {
					return strings.Contains(related.Message, "linux-amd64-default, windows-amd64-default")
				}) {
					t.Fatalf("unknown-module related selections = %#v", item.Related)
				}
			}
			if strings.Contains(item.Message, "allows unknown module example.com/servicefixture/internal/processcontainment") {
				t.Fatalf("known inactive module was rejected: %#v", result.Diagnostics().Items())
			}
		}
		if len(selected) != 1 {
			t.Fatalf("selection order %d unknown diagnostics = %#v", index, result.Diagnostics().Items())
		}
		if index == 0 {
			previousMessages = slices.Clone(selected)
		} else if !reflect.DeepEqual(previousMessages, selected) {
			t.Fatalf("selection-order diagnostics differ: %#v != %#v", previousMessages, selected)
		}
	}
}

func collapsedModuleConfiguration(sourceRoots []string) compilerstyle.Configuration {
	configuration := styleSelectionConfiguration(false)
	configuration.SourceRoots = slices.Clone(sourceRoots)
	configuration.GeneratedRoots = []string{"internal/spicegen"}
	configuration.BuildSelections = configuration.BuildSelections[:1]
	configuration.BuildSelections[0].SourceRoots = slices.Clone(sourceRoots)
	configuration.Rules.ModuleOwnership = compilerstyle.RuleLevelError
	return configuration
}

func writePlatformModuleUniverseFixture(
	t *testing.T,
) (string, compilerstyle.Configuration) {
	t.Helper()
	root := writeServiceModule(t)
	for _, relative := range []string{"main.go", "orders", "internal/spicegen"} {
		if err := os.RemoveAll(filepath.Join(root, filepath.FromSlash(relative))); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		"internal/app/containment_linux.go": `//go:build linux

package app

import (
	_ "example.com/servicefixture/internal/processcontainment"
	"example.com/servicefixture/internal/processcontainment/spi"
)

type Containment struct { Service spi.Service }
`,
		"internal/processcontainment/doc.go": `// @import { Module } from "github.com/spice-framework/spice/annotation/modulith"

// Package processcontainment owns platform process isolation.
// @Module
package processcontainment
`,
		"internal/processcontainment/spi/doc_linux.go": `//go:build linux

// @import { NamedInterface } from "github.com/spice-framework/spice/annotation/modulith"

// Package spi exposes platform process isolation.
// @NamedInterface("spi")
package spi
`,
		"internal/processcontainment/spi/service_linux.go": `//go:build linux

package spi

type Service struct{}
`,
	}
	for relative, content := range files {
		writeServiceFixtureFile(t, root, relative, content)
	}
	writePlatformApplicationModule(
		t,
		root,
		"example.com/servicefixture/internal/processcontainment",
		"example.com/servicefixture/internal/processcontainment::spi",
	)
	configuration := styleSelectionConfiguration(false)
	configuration.SourceRoots = []string{"internal"}
	configuration.GeneratedRoots = []string{"internal/spicegen"}
	for index := range configuration.BuildSelections {
		configuration.BuildSelections[index].SourceRoots = []string{"internal"}
	}
	configuration.Rules.ModuleOwnership = compilerstyle.RuleLevelError
	configuration.AllowedBoundaryFiles = []string{
		"internal/app/application.go",
		"internal/processcontainment/doc.go",
		"internal/processcontainment/spi/doc_linux.go",
	}
	configuration.PackageFunctionExceptions = []compilerstyle.PackageFunctionException{{
		Glob: "internal/app/application.go", ContributionKind: "application",
		Maximum: 1, Reason: "declarative application marker",
	}}
	return root, configuration
}

func writePlatformApplicationModule(t *testing.T, root string, dependencies ...string) {
	t.Helper()
	quoted := make([]string, len(dependencies))
	for index, dependency := range dependencies {
		quoted[index] = strconv.Quote(dependency)
	}
	writeServiceFixtureFile(t, root, "internal/app/application.go", `// @import { Application } from "github.com/spice-framework/spice/annotation/core"
// @import { Module } from "github.com/spice-framework/spice/annotation/modulith"

// Package app owns the platform-selected application.
// @Module(allowedDependencies=[`+strings.Join(quoted, ", ")+`])
package app

// @Application
func Application() {}
`)
}

func TestConfiguredStyleStopsAfterCancellationBetweenApplicationTargets(t *testing.T) {
	root, configuration := writeIndependentGeneratedApplications(t)
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	service, err := New(Config{Loader: func(
		loadContext context.Context,
		options load.Options,
		patterns ...string,
	) (*load.Program, error) {
		calls++
		if calls == 2 {
			cancel()
			return nil, loadContext.Err()
		}
		return load.Load(loadContext, options, patterns...)
	}})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceCleanup(t, service)
	_, err = service.Analyze(ctx, Request{
		WorkspaceRoot: root, Mode: AnalysisValidate,
		StyleConfiguration: &configuration,
	})
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("Analyze() cancellation error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("loader calls = %d, want 2", calls)
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

func writeIndependentGeneratedApplications(
	t *testing.T,
) (string, compilerstyle.Configuration) {
	t.Helper()
	root := writeServiceModule(t)
	for _, relative := range []string{
		"main.go",
		"orders/doc.go",
		"orders/config.go",
		"internal/spicegen/servicefixture/spice_command_gen.go",
	} {
		if err := os.Remove(filepath.Join(root, filepath.FromSlash(relative))); err != nil {
			t.Fatal(err)
		}
	}
	for _, target := range []string{"one", "two"} {
		writeServiceFixtureFile(t, root, "cmd/"+target+"/application.go", fmt.Sprintf(`//go:build spice_generate

package main

import (
	"os"

	_ "example.com/servicefixture/%[1]s"
	spiceapp "example.com/servicefixture/internal/spicegen/%[1]s"
)

// @import { Application } from "github.com/spice-framework/spice/annotation/core"

// @Application
func main() {
	os.Exit(spiceapp.Main(os.Args[1:]))
}
`, target))
		writeServiceFixtureFile(t, root, target+"/settings.go", `package `+target+`

// @import { ConfigurationProperties } from "github.com/spice-framework/spice/annotation/core"

// @ConfigurationProperties(prefix="agent")
type Settings struct {
	Workspace string `+"`spice:\"workspace,env=SPICE_AGENT_WORKSPACE\"`"+`
}
`)
	}
	falseValue := false
	disabled := compilerstyle.RuleLevelOff
	return root, compilerstyle.Configuration{
		SchemaVersion: 2,
		Profile:       string(compilerstyle.ProfileJavaStructured),
		SourceRoots:   []string{"cmd", "one", "two"},
		GeneratedRoots: []string{
			"cmd/internal/spicegen",
		},
		BuildSelections: []compilerstyle.BuildSelection{{
			Name: "linux-amd64-default", SourceRoots: []string{"cmd", "one", "two"},
			GOOS: "linux", GOARCH: "amd64", CGOEnabled: &falseValue,
		}},
		Rules: compilerstyle.Rules{
			OnePrimaryTypePerFile: disabled, MethodsInPrimaryFile: disabled,
			FileNameMatchesType: disabled, PackageFunctions: disabled,
			ExplicitConstructors: disabled, ExplicitManagedScopes: disabled,
			BanInit: disabled, BanMutablePackageState: disabled,
			PrivateManagedFields: disabled, ModuleOwnership: disabled,
			RouteClassification: disabled, ContextFirst: disabled,
			ErrorLast: disabled, MaxTypeFileLines: 500,
		},
		AllowedBoundaryFiles: []string{"cmd/*/application.go"},
		PackageFunctionExceptions: []compilerstyle.PackageFunctionException{{
			Glob: "cmd/*/application.go", ContributionKind: "application",
			Maximum: 1, Reason: "compiler-validated generated application bridge",
		}},
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

func FuzzConfiguredApplicationTargetsAreDeterministic(f *testing.F) {
	f.Add("example.com/app/one", "example.com/app/two")
	f.Add("same", "same")
	f.Fuzz(func(t *testing.T, ordinary, generated string) {
		applications := []string{ordinary, generated, ordinary}
		entrypoints := []load.GeneratedApplicationEntrypoint{
			{PackagePath: generated},
			{PackagePath: ordinary},
		}
		first := configuredApplicationTargets(applications, entrypoints)
		second := configuredApplicationTargets(applications, entrypoints)
		if !reflect.DeepEqual(first, second) {
			t.Fatalf("configuredApplicationTargets() is nondeterministic: %#v != %#v", first, second)
		}
		for index := 1; index < len(first); index++ {
			if first[index-1].packagePath >= first[index].packagePath {
				t.Fatalf("configuredApplicationTargets() is not strictly ordered: %#v", first)
			}
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

func BenchmarkConfiguredApplicationTargets(b *testing.B) {
	applications := make([]string, 64)
	entrypoints := make([]load.GeneratedApplicationEntrypoint, 64)
	for index := range applications {
		packagePath := "example.com/application/target" + strconv.Itoa(index)
		applications[index] = packagePath
		entrypoints[index].PackagePath = packagePath
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if targets := configuredApplicationTargets(applications, entrypoints); len(targets) != 64 {
			b.Fatalf("configuredApplicationTargets() length = %d", len(targets))
		}
	}
}
