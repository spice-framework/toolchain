package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/spice-framework/toolchain/compiler/application"
	"github.com/spice-framework/toolchain/compiler/load"
	"github.com/spice-framework/toolchain/internal/testsupport"
)

func TestRunGenerateApplyCheckAndDiff(t *testing.T) {
	root := generationCLIModule(t, generationApplicationSource)

	code, stdout, stderr := runModule(root, "generate", "./...")
	if code != 0 || !strings.Contains(stdout, "Spice generated target Application") || stderr != "" {
		t.Fatalf("generate: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	generatedPath := filepath.Join(
		root,
		"internal",
		"spicegen",
		"application",
		"spice_assembly_gen.go",
	)
	sourceUnitPath := filepath.Join(
		root,
		"internal",
		"spicegen",
		"application",
		"sources",
		"app",
		"application_spice_gen.go",
	)
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

	updatedSource, _ := withTestAnnotationImports(
		generationApplicationWithProviderSource,
		false,
	)
	if err := os.WriteFile(
		filepath.Join(root, "app", "application.go"),
		[]byte(updatedSource),
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
		!strings.Contains(stdout, "+\tvalue := app.ValueProvider()") ||
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
	if code != 0 || !strings.Contains(stdout, "wrote 4 file") || stderr != "" {
		t.Fatalf("regenerate: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	content, err := os.ReadFile(sourceUnitPath)
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
	renamedSource, _ := withTestAnnotationImports(
		renamedProvider,
		false,
	)
	if writeErr := os.WriteFile(
		filepath.Join(root, "app", "application.go"),
		[]byte(renamedSource),
		0o600,
	); writeErr != nil {
		t.Fatal(writeErr)
	}
	code, stdout, stderr = runModule(root, "generate", "./...")
	if code != 0 || !strings.Contains(stdout, "wrote 4 file") || stderr != "" {
		t.Fatalf("recover stale uncompilable output: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	content, err = os.ReadFile(sourceUnitPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "app.RenamedProvider()") {
		t.Fatalf("recovered generated source missing renamed provider:\n%s", content)
	}
}

func TestRunGenerateUsesApplicationScopedLocalModuleUniverse(t *testing.T) {
	root := generationModuleUniverseCLIModule(t, false)
	code, stdout, stderr := runModule(
		root,
		"generate", "--target", "ArchitectureProof", "./app",
	)
	if code != 0 || !strings.Contains(stdout, "generated target ArchitectureProof") || stderr != "" {
		t.Fatalf("generate: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runModule(
		root,
		"generate", "--check", "--target", "ArchitectureProof", "./app",
	)
	if code != 0 || !strings.Contains(stdout, "generation is current") || stderr != "" {
		t.Fatalf("generate --check: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	unknownRoot := generationModuleUniverseCLIModule(t, true)
	code, stdout, stderr = runModule(
		unknownRoot,
		"generate", "--target", "ArchitectureProof", "./app",
	)
	if code != 1 || stdout != "" ||
		!strings.Contains(stderr, "[spice.module.unknown-module]") ||
		!strings.Contains(stderr, "example.com/fixture/internal/missing") ||
		strings.Contains(stderr, "spice.load") {
		t.Fatalf("unknown module: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestRunGenerateCheckIsPortableAcrossTargetPlatforms(t *testing.T) {
	root := generationPlatformIdentityCLIModule(t)
	const target = "ArchitectureProof"
	code, stdout, stderr := runGenerationModuleWithEnvironment(
		root,
		generationTargetEnvironment("windows"),
		"generate", "--target", target, "./app",
	)
	if code != 0 || !strings.Contains(stdout, "generated target "+target) || stderr != "" {
		t.Fatalf("windows generate: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	assemblies, err := filepath.Glob(filepath.Join(
		root,
		"internal",
		"spicegen",
		"*",
		"spice_assembly_gen.go",
	))
	if err != nil || len(assemblies) != 1 {
		t.Fatalf("generated assemblies = %v, %v", assemblies, err)
	}
	assembly, err := os.ReadFile(assemblies[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, moduleID := range []string{
		"example.com/fixture/internal/poisonidentity",
		"example.com/fixture/internal/processcontainment",
		"example.com/fixture/internal/processleaf",
	} {
		line := "{Module: \"" + moduleID + "\"}"
		if count := strings.Count(string(assembly), line); count != 1 {
			t.Fatalf("logging identity %q count = %d, want 1", moduleID, count)
		}
	}
	for _, poison := range []string{
		"ForeignApplication",
		"NewForeignService",
		"PoisonSettings",
		"poison.enabled",
	} {
		if strings.Contains(string(assembly), poison) {
			t.Fatalf("generated assembly merged poison %q", poison)
		}
	}

	manifestPath := filepath.Join(root, ".spice", "architectureproof.manifest.json")
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, goos := range []string{"linux", "darwin", "windows"} {
		code, stdout, stderr = runGenerationModuleWithEnvironment(
			root,
			generationTargetEnvironment(goos),
			"generate", "--check", "--diff", "--target", target, "./app",
		)
		if code != 0 || !strings.Contains(stdout, "generation is current") || stderr != "" {
			t.Fatalf("%s check: code=%d stdout=%q stderr=%q", goos, code, stdout, stderr)
		}
		currentAssembly, readErr := os.ReadFile(assemblies[0])
		if readErr != nil || !slices.Equal(currentAssembly, assembly) {
			t.Fatalf("%s check changed assembly bytes: %v", goos, readErr)
		}
		currentManifest, readErr := os.ReadFile(manifestPath)
		if readErr != nil || !slices.Equal(currentManifest, manifest) {
			t.Fatalf("%s check changed manifest bytes: %v", goos, readErr)
		}
	}
}

func runGenerationModuleWithEnvironment(
	root string,
	environment []string,
	arguments ...string,
) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(
		arguments,
		&stdout,
		&stderr,
		load.Options{Dir: root, Env: environment},
		load.Load,
	)
	return code, stdout.String(), stderr.String()
}

func generationTargetEnvironment(goos string) []string {
	result := slices.Clone(os.Environ())
	for _, setting := range []struct {
		name  string
		value string
	}{
		{name: "CGO_ENABLED", value: "0"},
		{name: "GOARCH", value: "amd64"},
		{name: "GOENV", value: "off"},
		{name: "GOFLAGS", value: ""},
		{name: "GOOS", value: goos},
		{name: "GOPROXY", value: "off"},
		{name: "GOSUMDB", value: "off"},
		{name: "GOTOOLCHAIN", value: "local"},
	} {
		prefix := setting.name + "="
		replaced := false
		for index, entry := range result {
			if strings.HasPrefix(strings.ToUpper(entry), prefix) {
				result[index] = prefix + setting.value
				replaced = true
			}
		}
		if !replaced {
			result = append(result, prefix+setting.value)
		}
	}
	return result
}

func generationPlatformIdentityCLIModule(t *testing.T) string {
	t.Helper()
	return writeModule(t, map[string]string{
		"app/application.go": `package app

import _ "example.com/fixture/internal/processplatform"

// @Application
// @observability.Logging
func ArchitectureProof() {}
`,
		"app/doc.go": `// @Module(allowedDependencies=["example.com/fixture/internal/poisonidentity", "example.com/fixture/internal/processplatform"])
package app
`,
		"internal/processplatform/doc.go": `// @Module(allowedDependencies=["example.com/fixture/internal/processcontainment"])
package processplatform
`,
		"internal/processplatform/process_unix.go": `//go:build linux || darwin

package processplatform

import _ "example.com/fixture/internal/processcontainment"
`,
		"internal/processplatform/process_windows.go": `//go:build windows

package processplatform
`,
		"internal/processcontainment/doc.go": `// @Module(allowedDependencies=["example.com/fixture/internal/processleaf"])
package processcontainment
`,
		"internal/processleaf/doc.go": `// @Module
package processleaf
`,
		"internal/poisonidentity/doc.go": `// @Module
package poisonidentity
`,
		"internal/poisonidentity/poison.go": `// @import { Application, Bean, ConfigurationProperties, Singleton } from "github.com/spice-framework/spice/annotation/core"

package poisonidentity

// @ConfigurationProperties(prefix="poison")
type PoisonSettings struct {
	Enabled bool ` + "`spice:\"enabled,default=false\"`" + `
}

type ForeignService struct{}

// @Bean
// @Singleton
func NewForeignService(PoisonSettings) *ForeignService {
	panic("identity inventory must not execute or join provider bodies")
}

// @Application
func ForeignApplication(*ForeignService) {}
`,
	})
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

func TestRunGenerateBuildsPreferredPackageMainWithExplicitGeneratedImport(t *testing.T) {
	repository := testsupport.CoreDirectory(t)
	root := writeModule(t, map[string]string{
		"go.mod": "module example.com/package-main\n\ngo 1.26.0\n\n" +
			"require github.com/spice-framework/spice v0.0.0\n\n" +
			"replace github.com/spice-framework/spice => " +
			filepath.ToSlash(repository) + "\n",
		"cmd/shop/main.go": `package main

import (
	"os"

	spiceapp "example.com/package-main/internal/spicegen/shop"
	_ "example.com/package-main/platform"
)

// @Application
// @management.Enable(expose=["health"])
func main() {
	os.Exit(spiceapp.Main(os.Args[1:]))
}
`,
		"platform/platform.go": `// Package platform owns HTTP setup.
//
// @Module
package platform

import "net/http"

// @Bean
func Mux() *http.ServeMux {
	return http.NewServeMux()
}
`,
	})

	code, stdout, stderr := runModule(root, "generate", "./cmd/shop")
	if code != 0 ||
		!strings.Contains(stdout, "generated target Shop") ||
		stderr != "" {
		t.Fatalf(
			"generate: code=%d stdout=%q stderr=%q",
			code,
			stdout,
			stderr,
		)
	}
	generatedPath := filepath.Join(
		root,
		"internal",
		"spicegen",
		"shop",
		"spice_assembly_gen.go",
	)
	content, err := os.ReadFile(generatedPath)
	if err != nil {
		t.Fatal(err)
	}
	mainUnit, err := os.ReadFile(filepath.Join(
		root,
		"internal",
		"spicegen",
		"shop",
		"sources",
		"cmd",
		"shop",
		"main_spice_gen.go",
	))
	if err != nil {
		t.Fatal(err)
	}
	sourceUnit, err := os.ReadFile(filepath.Join(
		root,
		"internal",
		"spicegen",
		"shop",
		"sources",
		"platform",
		"platform_spice_gen.go",
	))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "package spicegen") ||
		!strings.Contains(
			string(mainUnit),
			` = "shop"`,
		) ||
		!strings.Contains(string(sourceUnit), "platform.Mux()") {
		t.Fatalf("generated package-main source:\n%s", content)
	}

	code, stdout, stderr = runModule(root, "build", "./cmd/shop")
	if code != 0 ||
		!strings.Contains(stdout, "Spice build passed for target Shop") ||
		stderr != "" {
		t.Fatalf(
			"build: code=%d stdout=%q stderr=%q",
			code,
			stdout,
			stderr,
		)
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
	if _, err := os.Stat(filepath.Join(
		root,
		"internal",
		"spicegen",
		"beta",
		"spice_assembly_gen.go",
	)); err != nil {
		t.Fatalf("selected generated file: %v", err)
	}

	code, _, stderr = runModule(root, "generate", "--target=Missing", "./...")
	if code != 1 || !strings.Contains(stderr, `target "Missing" was not found`) {
		t.Fatalf("missing target: code=%d stderr=%q", code, stderr)
	}
}

func TestRunGenerateRelocatesVerifiedModuleOwnership(t *testing.T) {
	root := generationCLIModule(t, generationApplicationSource)
	code, stdout, stderr := runModule(root, "generate", "./app")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "generated target Application") {
		t.Fatalf("initial generate: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	goModPath := filepath.Join(root, "go.mod")
	content, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatal(err)
	}
	content = []byte(strings.Replace(
		string(content),
		"module example.com/cli-generation",
		"module example.com/cli-relocated",
		1,
	))
	if err := os.WriteFile(goModPath, content, 0o600); err != nil {
		t.Fatal(err)
	}

	code, _, stderr = runModule(root, "generate", "./app")
	if code != 1 || !strings.Contains(stderr, "target does not match") {
		t.Fatalf("unapproved relocation: code=%d stderr=%q", code, stderr)
	}
	code, stdout, stderr = runModule(
		root,
		"generate",
		"--relocate-module-from",
		"example.com/cli-generation",
		"./app",
	)
	if code != 0 || stderr != "" || !strings.Contains(stdout, "generated target Application") {
		t.Fatalf("approved relocation: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runModule(root, "generate", "--check", "./app")
	if code != 0 || stderr != "" || !strings.Contains(stdout, "is current") {
		t.Fatalf("relocated check: code=%d stdout=%q stderr=%q", code, stdout, stderr)
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
		{"generate", "--relocate-module-from"},
		{"generate", "--relocate-module-from="},
		{"generate", "--relocate-module-from=example.com/old", "--relocate-module-from", "example.com/older"},
		{"generate", "--check", "--relocate-module-from", "example.com/old"},
		{"generate", "--diff", "--relocate-module-from=example.com/old"},
		{"build", "--check"},
		{"build", "--diff"},
		{"build", "--relocate-module-from", "example.com/old"},
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
		{Name: "Web", SymbolID: "web-id", PackagePath: "example.com/web"},
		{Name: "Worker", SymbolID: "worker-id", PackagePath: "example.com/worker"},
	}
	target, err := selectApplicationTarget(targets, "worker-id")
	if err != nil || target.Name != "Worker" {
		t.Fatalf("selectApplicationTarget() = %#v, %v", target, err)
	}
	target, err = selectApplicationTarget(targets, "example.com/web")
	if err != nil || target.Name != "Web" {
		t.Fatalf("selectApplicationTarget(import path) = %#v, %v", target, err)
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
	if !result.PrepareGeneratedApplicationEntrypoints {
		t.Fatal("withAnalysisBuildTag() disabled generated entrypoint preparation")
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
			"require github.com/spice-framework/spice v0.0.0\n\n" +
			"replace github.com/spice-framework/spice => " + filepath.ToSlash(repository) + "\n",
		"app/application.go": source,
	})
}

func generationModuleUniverseCLIModule(t *testing.T, unknown bool) string {
	t.Helper()
	dependencies := []string{
		"example.com/fixture/internal/processplatform",
		"example.com/fixture/internal/runidentity",
		"example.com/fixture/internal/workspace",
	}
	if unknown {
		dependencies = append(dependencies, "example.com/fixture/internal/missing")
		slices.Sort(dependencies)
	}
	quoted := make([]string, len(dependencies))
	for index, dependency := range dependencies {
		quoted[index] = strconv.Quote(dependency)
	}
	return writeModule(t, map[string]string{
		"app/application.go": `package app

import (
	_ "example.com/fixture/internal/processplatform"
	_ "example.com/fixture/internal/runidentity"
	_ "example.com/fixture/internal/workspace"
)

type Proof struct{}

// @Bean
// @Singleton
func NewProof() *Proof { return &Proof{} }

// @Application
func ArchitectureProof(*Proof) {}
`,
		"app/doc.go": `// Package app owns the selected application.
//
// @Module(allowedDependencies=[` + strings.Join(quoted, ", ") + `])
package app
`,
		"internal/processplatform/doc.go": `// Package processplatform owns platform behavior.
//
// @Module(allowedDependencies=["example.com/fixture/internal/processcontainment"])
package processplatform
`,
		"internal/processcontainment/doc.go": `// Package processcontainment owns inactive platform behavior.
//
// @Module
package processcontainment
`,
		"internal/runidentity/doc.go": `// Package runidentity owns run identity.
//
// @Module
package runidentity
`,
		"internal/workspace/doc.go": `// Package workspace owns workspace identity.
//
// @Module
package workspace
`,
	})
}
