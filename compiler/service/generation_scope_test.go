package service

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/spice-framework/toolchain/compiler/load"
	compilerstyle "github.com/spice-framework/toolchain/compiler/style"
)

func TestOrdinaryGenerationPromotesLocalModulesAndBuildsExactUniverse(t *testing.T) {
	root := writeGenerationModuleScopeFixture(t, false)
	var promotedLoads int
	hostile := append(
		os.Environ(),
		"GOAUTH=netrc",
		"GOENV="+filepath.Join(t.TempDir(), "hostile-goenv"),
		"GOEXPERIMENT=ambientexperiment",
		"GOFIPS140=latest",
		"GOPROXY=http://127.0.0.1:1",
		"GOSUMDB=invalid.example",
		"GOTOOLCHAIN=go1.99.0+auto",
	)
	service, err := New(Config{
		LoadOptions: load.Options{Env: hostile},
		Loader: func(
			ctx context.Context,
			options load.Options,
			patterns ...string,
		) (*load.Program, error) {
			assertGenerationEnvironmentIsOffline(t, options.Env)
			program, loadErr := load.Load(ctx, options, patterns...)
			if options.PromoteApplicationDependencies &&
				slices.Equal(patterns, []string{"./internal/architectureproof"}) &&
				program != nil {
				promotedLoads++
				for _, pkg := range program.PrimaryPackages() {
					if pkg.Raw == nil || pkg.Raw.Fset == nil || pkg.Types == nil ||
						pkg.TypesInfo == nil || len(pkg.Syntax) == 0 {
						t.Fatalf("promoted package %s is not syntax-complete", pkg.Path)
					}
				}
			}
			return program, loadErr
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceCleanup(t, service)

	result, err := service.Analyze(t.Context(), Request{
		WorkspaceRoot: root,
		Patterns:      []string{"./internal/architectureproof"},
		Target:        "ArchitectureProof",
		Mode:          AnalysisGenerate,
	})
	if err != nil {
		t.Fatal(err)
	}
	if diagnostics := result.Diagnostics().Items(); len(diagnostics) != 0 {
		t.Fatalf("ordinary generation diagnostics = %#v", diagnostics)
	}
	if _, found := result.GenerationPlan(); !found {
		t.Fatal("ordinary generation plan was not produced")
	}
	if promotedLoads != 2 {
		t.Fatalf("syntax-complete target loads = %d, want 2", promotedLoads)
	}
	if targets := result.ApplicationModel().Targets(); len(targets) != 1 ||
		targets[0].Name != "ArchitectureProof" {
		t.Fatalf("ordinary generation targets = %#v", targets)
	}
	modules := result.ModuleGraph().Modules
	if got := moduleIDs(modules); !slices.Equal(got, []string{
		"example.com/servicefixture/internal/architectureproof",
		"example.com/servicefixture/internal/processplatform",
		"example.com/servicefixture/internal/runidentity",
		"example.com/servicefixture/internal/workspace",
	}) {
		t.Fatalf("active generation modules = %v", got)
	}

	configuration := generationScopeStyleConfiguration()
	styleService, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceCleanup(t, styleService)
	styled, err := styleService.Analyze(t.Context(), Request{
		WorkspaceRoot:      root,
		Mode:               AnalysisValidate,
		StyleConfiguration: &configuration,
	})
	if err != nil {
		t.Fatal(err)
	}
	if diagnostics := styled.Diagnostics().Items(); len(diagnostics) != 0 {
		t.Fatalf("configured style diagnostics = %#v", diagnostics)
	}
	if scopes := styled.ApplicationScopes(); len(scopes) != 1 ||
		scopes[0].PackagePath != "example.com/servicefixture/internal/architectureproof" {
		t.Fatalf("configured style scopes = %#v", scopes)
	}
}

func assertGenerationEnvironmentIsOffline(t *testing.T, environment []string) {
	t.Helper()
	want := map[string]string{
		"GOAUTH": "off", "GOENV": "off", "GOEXPERIMENT": "",
		"GOFIPS140": "off", "GOPROXY": "off", "GOSUMDB": "off",
		"GOTOOLCHAIN": "local",
	}
	for name, value := range want {
		found := false
		for _, entry := range environment {
			key, setting, present := strings.Cut(entry, "=")
			if present && strings.EqualFold(key, name) {
				if found || setting != value {
					t.Fatalf("generation environment %s = %q, want exactly %q", name, setting, value)
				}
				found = true
			}
		}
		if !found {
			t.Fatalf("generation environment lacks %s", name)
		}
	}
}

func TestOrdinaryGenerationKeepsUnknownModuleDiagnostic(t *testing.T) {
	root := writeGenerationModuleScopeFixture(t, true)
	service, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceCleanup(t, service)
	var previous string
	for attempt := 0; attempt < 2; attempt++ {
		result, analyzeErr := service.Analyze(t.Context(), Request{
			WorkspaceRoot: root,
			Patterns:      []string{"./internal/architectureproof"},
			Target:        "ArchitectureProof",
			Mode:          AnalysisGenerate,
		})
		if analyzeErr != nil {
			t.Fatal(analyzeErr)
		}
		diagnostics := result.Diagnostics().Items()
		if len(diagnostics) != 1 || diagnostics[0].Code != "spice.module.unknown-module" ||
			!strings.Contains(diagnostics[0].Message, "internal/missing") ||
			strings.Contains(diagnostics[0].Message, "load") {
			t.Fatalf("unknown module diagnostics = %#v", diagnostics)
		}
		if attempt == 0 {
			previous = diagnostics[0].Message
		} else if diagnostics[0].Message != previous {
			t.Fatalf("unknown module diagnostics changed: %q != %q", diagnostics[0].Message, previous)
		}
	}
}

func TestOrdinaryGenerationWithoutModuleDependenciesKeepsOneAnalysis(t *testing.T) {
	root := writeServiceModule(t)
	loads := 0
	service, err := New(Config{Loader: func(
		ctx context.Context,
		options load.Options,
		patterns ...string,
	) (*load.Program, error) {
		loads++
		return load.Load(ctx, options, patterns...)
	}})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceCleanup(t, service)
	result, err := service.Analyze(t.Context(), Request{
		WorkspaceRoot: root,
		Patterns:      []string{"./..."},
		Mode:          AnalysisGenerate,
	})
	if err != nil {
		t.Fatal(err)
	}
	if diagnostics := result.Diagnostics().Items(); len(diagnostics) != 0 ||
		!result.GenerationReady() || loads != 1 {
		t.Fatalf(
			"no-dependency generation diagnostics=%#v ready=%t loads=%d",
			diagnostics,
			result.GenerationReady(),
			loads,
		)
	}
}

func TestOrdinaryGenerationModuleInventoryIsSortedAndCancellable(t *testing.T) {
	root := writeGenerationModuleScopeFixture(t, false)
	writeServiceFixtureFile(t, root, "internal/architectureproof/doc.go", `// @import { Module } from "github.com/spice-framework/spice/annotation/modulith"

// Package architectureproof owns the selected application.
// @Module(allowedDependencies=["example.com/servicefixture/internal/zeta", "example.com/servicefixture/internal/alpha"])
package architectureproof
`)
	for _, dependency := range []string{"alpha", "zeta"} {
		writeServiceFixtureFile(t, root, "internal/"+dependency+"/doc.go", `// @import { Module } from "github.com/spice-framework/spice/annotation/modulith"

// @Module
package `+dependency+`
`)
	}

	ctx, cancel := context.WithCancel(t.Context())
	var inventories []string
	service, err := New(Config{Loader: func(
		loadContext context.Context,
		options load.Options,
		patterns ...string,
	) (*load.Program, error) {
		if len(patterns) == 1 && strings.HasPrefix(patterns[0], "example.com/servicefixture/internal/") {
			inventories = append(inventories, patterns[0])
			if strings.HasSuffix(patterns[0], "/zeta") {
				cancel()
				return nil, loadContext.Err()
			}
		}
		return load.Load(loadContext, options, patterns...)
	}})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceCleanup(t, service)
	_, err = service.Analyze(ctx, Request{
		WorkspaceRoot: root,
		Patterns:      []string{"./internal/architectureproof"},
		Target:        "ArchitectureProof",
		Mode:          AnalysisGenerate,
	})
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("generation cancellation error = %v", err)
	}
	if !slices.Equal(inventories, []string{
		"example.com/servicefixture/internal/alpha",
		"example.com/servicefixture/internal/processcontainment",
		"example.com/servicefixture/internal/zeta",
	}) {
		t.Fatalf("module inventory order = %v", inventories)
	}
}

func TestOrdinaryGenerationModuleInventoryIsTargetLocal(t *testing.T) {
	root := writeGenerationModuleScopeFixture(t, false)
	writeServiceFixtureFile(t, root, "internal/processcontainment/doc.go", `// @import { Module } from "github.com/spice-framework/spice/annotation/modulith"

// Package processcontainment owns an inactive platform dependency.
// @Module(allowedDependencies=["example.com/servicefixture/internal/processplatform"])
package processcontainment
`)
	writeServiceFixtureFile(t, root, "internal/processcontainment/application.go", `package processcontainment

// @import { Application } from "github.com/spice-framework/spice/annotation/core"

// @Application
func OtherApplication() {}
`)
	service, err := New(Config{})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceCleanup(t, service)
	result, err := service.Analyze(t.Context(), Request{
		WorkspaceRoot: root,
		Patterns:      []string{"./internal/architectureproof"},
		Target:        "ArchitectureProof",
		Mode:          AnalysisGenerate,
	})
	if err != nil {
		t.Fatal(err)
	}
	if diagnostics := result.Diagnostics().Items(); len(diagnostics) != 0 {
		t.Fatalf("target-local generation diagnostics = %#v", diagnostics)
	}
	if targets := result.ApplicationModel().Targets(); len(targets) != 1 ||
		targets[0].Name != "ArchitectureProof" {
		t.Fatalf("dependency inventory polluted targets = %#v", targets)
	}
}

func TestOrdinaryGenerationDiscoversNamedInterfaceIdentity(t *testing.T) {
	root := writeGenerationModuleScopeFixture(t, false)
	writeServiceFixtureFile(t, root, "internal/processplatform/doc.go", `// @import { Module } from "github.com/spice-framework/spice/annotation/modulith"

// Package processplatform owns platform behavior.
// @Module(allowedDependencies=["example.com/servicefixture/internal/processcontainment::spi"])
package processplatform
`)
	interfacePath := filepath.Join(root, "internal", "processcontainment", "spi", "doc.go")
	writeServiceFixtureFile(t, root, "internal/processcontainment/spi/doc.go", `// Package spi is overlaid during generation.
package spi
`)
	interfaceSource := `// @import { NamedInterface } from "github.com/spice-framework/spice/annotation/modulith"

// Package spi exposes process containment.
// @NamedInterface("spi")
package spi
`
	ignoredInterface := `// @import { NamedInterface } from "github.com/spice-framework/spice/annotation/modulith"

// @NamedInterface("ignored")
package ignored
`
	for _, relative := range []string{
		"internal/processcontainment/vendor/ignored/doc.go",
		"internal/processcontainment/testdata/ignored/doc.go",
		"internal/processcontainment/.hidden/ignored/doc.go",
		"internal/processcontainment/_ignored/doc.go",
		"internal/processcontainment/internal/spicegen/ignored/doc.go",
	} {
		writeServiceFixtureFile(t, root, relative, ignoredInterface)
	}
	writeServiceFixtureFile(
		t,
		root,
		"internal/processcontainment/generated/ignored/doc.go",
		"// Code generated by Spice. DO NOT EDIT.\n\n"+ignoredInterface,
	)
	writeServiceFixtureFile(
		t,
		root,
		"internal/processcontainment/nested/go.mod",
		"module example.com/nested\n\ngo 1.26.5\n",
	)
	writeServiceFixtureFile(
		t,
		root,
		"internal/processcontainment/nested/ignored/doc.go",
		ignoredInterface,
	)
	loadedInterfaces := make(map[string]struct{})
	service, err := New(Config{Loader: func(
		ctx context.Context,
		options load.Options,
		patterns ...string,
	) (*load.Program, error) {
		for _, pattern := range patterns {
			if strings.HasPrefix(
				pattern,
				"example.com/servicefixture/internal/processcontainment/",
			) {
				loadedInterfaces[pattern] = struct{}{}
			}
		}
		return load.Load(ctx, options, patterns...)
	}})
	if err != nil {
		t.Fatal(err)
	}
	registerServiceCleanup(t, service)
	result, err := service.Analyze(t.Context(), Request{
		WorkspaceRoot: root,
		Patterns:      []string{"./internal/architectureproof"},
		Target:        "ArchitectureProof",
		Mode:          AnalysisGenerate,
		Overlay: map[string]Document{
			interfacePath: {Version: 7, Content: []byte(interfaceSource)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if diagnostics := result.Diagnostics().Items(); len(diagnostics) != 0 {
		t.Fatalf("named-interface generation diagnostics = %#v", diagnostics)
	}
	got := make([]string, 0, len(loadedInterfaces))
	for packagePath := range loadedInterfaces {
		got = append(got, packagePath)
	}
	slices.Sort(got)
	if !slices.Equal(got, []string{
		"example.com/servicefixture/internal/processcontainment/spi",
	}) {
		t.Fatalf("named-interface inventory escaped its validated subtree: %v", got)
	}
}

func TestOrdinaryGenerationRejectsUntrustedModuleIdentities(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*testing.T, string)
		want    string
	}{
		{
			name: "external import path",
			prepare: func(t *testing.T, root string) {
				t.Helper()
				replaceGenerationFixtureDependency(t, root, "example.net/external")
			},
			want: "example.net/external",
		},
		{
			name: "malformed local module",
			prepare: func(t *testing.T, root string) {
				t.Helper()
				writeServiceFixtureFile(t, root, "internal/missing/doc.go", `// @import { Module } from "github.com/spice-framework/spice/annotation/modulith"

// @Module(allowedDependencies=[1])
package missing
`)
			},
			want: "internal/missing",
		},
		{
			name: "unannotated local package",
			prepare: func(t *testing.T, root string) {
				t.Helper()
				writeServiceFixtureFile(
					t,
					root,
					"internal/missing/service.go",
					"package missing\n\ntype Service struct{}\n",
				)
			},
			want: "internal/missing",
		},
		{
			name: "symlink escape",
			prepare: func(t *testing.T, root string) {
				t.Helper()
				replaceGenerationFixtureDependency(t, root, "example.com/servicefixture/internal/escaped")
				outside := t.TempDir()
				writeServiceFixtureFile(t, outside, "doc.go", `// @import { Module } from "github.com/spice-framework/spice/annotation/modulith"

// @Module
package escaped
`)
				link := filepath.Join(root, "internal", "escaped")
				if err := os.Symlink(outside, link); err != nil {
					t.Skipf("directory symlink unavailable: %v", err)
				}
			},
			want: "internal/escaped",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := writeGenerationModuleScopeFixture(t, true)
			test.prepare(t, root)
			service, err := New(Config{})
			if err != nil {
				t.Fatal(err)
			}
			registerServiceCleanup(t, service)
			result, err := service.Analyze(t.Context(), Request{
				WorkspaceRoot: root,
				Patterns:      []string{"./internal/architectureproof"},
				Target:        "ArchitectureProof",
				Mode:          AnalysisGenerate,
			})
			if err != nil {
				t.Fatal(err)
			}
			diagnostics := result.Diagnostics().Items()
			if len(diagnostics) != 1 || diagnostics[0].Code != "spice.module.unknown-module" ||
				!strings.Contains(diagnostics[0].Message, test.want) {
				t.Fatalf("untrusted module diagnostics = %#v", diagnostics)
			}
		})
	}
}

func replaceGenerationFixtureDependency(t *testing.T, root, dependency string) {
	t.Helper()
	path := filepath.Join(root, "internal", "architectureproof", "doc.go")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := strings.Replace(
		string(content),
		"example.com/servicefixture/internal/missing",
		dependency,
		1,
	)
	if updated == string(content) {
		t.Fatal("missing fixture dependency was not replaced")
	}
	if err := os.WriteFile(path, []byte(updated), 0o600); err != nil {
		t.Fatal(err)
	}
}

func generationScopeStyleConfiguration() compilerstyle.Configuration {
	falseValue := false
	off := compilerstyle.RuleLevelOff
	return compilerstyle.Configuration{
		SchemaVersion: 2,
		Profile:       string(compilerstyle.ProfileJavaStructured),
		SourceRoots:   []string{"internal"},
		GeneratedRoots: []string{
			"internal/spicegen",
		},
		BuildSelections: []compilerstyle.BuildSelection{{
			Name: "host-generation-parity", SourceRoots: []string{"internal"},
			GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, CGOEnabled: &falseValue,
			Tags: []string{},
		}},
		Rules: compilerstyle.Rules{
			OnePrimaryTypePerFile: off, MethodsInPrimaryFile: off,
			FileNameMatchesType: off, PackageFunctions: off,
			ExplicitConstructors: off, ExplicitManagedScopes: off,
			BanInit: off, BanMutablePackageState: off,
			PrivateManagedFields: off,
			ModuleOwnership:      compilerstyle.RuleLevelError,
			RouteClassification:  off, ContextFirst: off, ErrorLast: off,
			MaxTypeFileLines: 500,
		},
		AllowedBoundaryFiles: []string{
			"**/*_bean.go",
			"**/doc.go",
			"internal/architectureproof/application.go",
		},
		PackageFunctionExceptions: []compilerstyle.PackageFunctionException{
			{
				Glob: "**/*_bean.go", ContributionKind: "provider",
				Maximum: 1, Reason: "dedicated provider fixture",
			},
			{
				Glob: "internal/architectureproof/application.go", ContributionKind: "application",
				Maximum: 1, Reason: "exact application fixture",
			},
		},
	}
}

func writeGenerationModuleScopeFixture(t *testing.T, unknown bool) string {
	t.Helper()
	root := writeServiceModule(t)
	for _, relative := range []string{"main.go", "orders", "internal/spicegen"} {
		if err := os.RemoveAll(filepath.Join(root, filepath.FromSlash(relative))); err != nil {
			t.Fatal(err)
		}
	}
	dependencies := []string{
		"example.com/servicefixture/internal/processplatform",
		"example.com/servicefixture/internal/runidentity",
		"example.com/servicefixture/internal/workspace",
	}
	if unknown {
		dependencies = append(dependencies, "example.com/servicefixture/internal/missing")
		slices.Sort(dependencies)
	}
	quoted := make([]string, len(dependencies))
	for index, dependency := range dependencies {
		quoted[index] = strconv.Quote(dependency)
	}
	files := map[string]string{
		"internal/architectureproof/doc.go": `// @import { Module } from "github.com/spice-framework/spice/annotation/modulith"

// Package architectureproof owns the selected application.
// @Module(allowedDependencies=[` + strings.Join(quoted, ", ") + `])
package architectureproof
`,
		"internal/architectureproof/application.go": `package architectureproof

// @import { Application } from "github.com/spice-framework/spice/annotation/core"

// @Application
func ArchitectureProof(*Proof) {}
`,
		"internal/architectureproof/proof.go": `package architectureproof

import (
	"example.com/servicefixture/internal/processplatform"
	"example.com/servicefixture/internal/runidentity"
	"example.com/servicefixture/internal/workspace"
)

type Proof struct {
	Platform processplatform.Service
	Run runidentity.Service
	Workspace workspace.Service
}
`,
		"internal/architectureproof/proof_bean.go": `package architectureproof

// @import { Bean, Singleton } from "github.com/spice-framework/spice/annotation/core"

// @Bean
// @Singleton
func NewProof() *Proof { return &Proof{} }
`,
		"internal/processplatform/doc.go": `// @import { Module } from "github.com/spice-framework/spice/annotation/modulith"

// Package processplatform owns platform behavior.
// @Module(allowedDependencies=["example.com/servicefixture/internal/processcontainment"])
package processplatform
`,
		"internal/processplatform/service.go": "package processplatform\n\ntype Service struct{}\n",
		"internal/processcontainment/doc.go": `// @import { Module } from "github.com/spice-framework/spice/annotation/modulith"

// Package processcontainment owns an inactive platform dependency.
// @Module
package processcontainment
`,
		"internal/runidentity/doc.go": `// @import { Module } from "github.com/spice-framework/spice/annotation/modulith"

// @Module
package runidentity
`,
		"internal/runidentity/service.go": "package runidentity\n\ntype Service struct{}\n",
		"internal/workspace/doc.go": `// @import { Module } from "github.com/spice-framework/spice/annotation/modulith"

// @Module
package workspace
`,
		"internal/workspace/service.go": "package workspace\n\ntype Service struct{}\n",
	}
	for relative, content := range files {
		writeServiceFixtureFile(t, root, relative, content)
	}
	return root
}

func moduleIDs(modules []Module) []string {
	result := make([]string, len(modules))
	for index := range modules {
		result[index] = modules[index].ID
	}
	return result
}
