// Package bootstrapcheck proves that the checked-in Spice compiler can rebuild
// and execute an application from valid handwritten Go with no generated
// output present.
package bootstrapcheck

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
)

const spiceModule = "github.com/StevenBuglione/spice"

type commandRunner func(
	context.Context,
	string,
	[]string,
	string,
	...string,
) ([]byte, error)

// Run performs the repository bootstrap and generated-output recovery proof.
// It builds the compiler from non-generated packages, creates an isolated
// offline application, generates it twice from zero output, compares every
// owned byte, executes the result, and proves manual edits fail closed.
func Run(ctx context.Context, repositoryRoot string) error {
	return run(ctx, repositoryRoot, execute)
}

func run(
	ctx context.Context,
	repositoryRoot string,
	runner commandRunner,
) error {
	if ctx == nil {
		return errors.New("bootstrap check: context is nil")
	}
	if runner == nil {
		return errors.New("bootstrap check: command runner is nil")
	}
	proof, err := newProof(repositoryRoot, runner)
	if err != nil {
		return err
	}
	proofErr := proof.run(ctx)
	if cleanupErr := os.RemoveAll(proof.temporaryRoot); cleanupErr != nil {
		cleanupErr = fmt.Errorf(
			"bootstrap check: remove temporary root: %w",
			cleanupErr,
		)
		proofErr = errors.Join(proofErr, cleanupErr)
	}
	return proofErr
}

type proof struct {
	repositoryRoot       string
	temporaryRoot        string
	fixtureRoot          string
	spiceExecutable      string
	productionExecutable string
	executableSuffix     string
	environment          []string
	runner               commandRunner
}

func newProof(repositoryRoot string, runner commandRunner) (proof, error) {
	root, err := filepath.Abs(repositoryRoot)
	if err != nil {
		return proof{}, fmt.Errorf(
			"bootstrap check: resolve repository root: %w",
			err,
		)
	}
	if _, statErr := os.Stat(filepath.Join(root, "go.mod")); statErr != nil {
		return proof{}, fmt.Errorf(
			"bootstrap check: inspect repository module: %w",
			statErr,
		)
	}
	temporaryRoot, err := os.MkdirTemp("", "spice-bootstrap-check-")
	if err != nil {
		return proof{}, fmt.Errorf(
			"bootstrap check: create temporary root: %w",
			err,
		)
	}
	executableSuffix := ""
	if runtime.GOOS == "windows" {
		executableSuffix = ".exe"
	}
	return proof{
		repositoryRoot: root,
		temporaryRoot:  temporaryRoot,
		fixtureRoot:    filepath.Join(temporaryRoot, "application"),
		spiceExecutable: filepath.Join(
			temporaryRoot,
			"spice-bootstrap"+executableSuffix,
		),
		productionExecutable: filepath.Join(
			temporaryRoot,
			"spice-production"+executableSuffix,
		),
		executableSuffix: executableSuffix,
		environment:      offlineEnvironment(os.Environ()),
		runner:           runner,
	}, nil
}

func (p proof) run(ctx context.Context) error {
	if err := p.buildCompiler(ctx); err != nil {
		return err
	}
	if err := p.proveProductionDogfood(ctx); err != nil {
		return err
	}
	if err := writeFixture(p.fixtureRoot, p.repositoryRoot); err != nil {
		return err
	}
	baseline, err := p.generateBaseline(ctx)
	if err != nil {
		return err
	}
	if err := p.proveZeroOutputRegeneration(ctx, baseline); err != nil {
		return err
	}
	return p.proveManualEditRecovery(ctx, baseline)
}

func (p proof) buildCompiler(ctx context.Context) error {
	output, err := p.runner(
		ctx,
		p.repositoryRoot,
		p.environment,
		"go",
		"build",
		"-mod=vendor",
		"-trimpath",
		"-o",
		p.spiceExecutable,
		"./cmd/spice-bootstrap",
	)
	if err != nil {
		return commandError("build independent Spice compiler", output, err)
	}
	dependencies, err := p.runner(
		ctx,
		p.repositoryRoot,
		p.environment,
		"go",
		"list",
		"-mod=vendor",
		"-deps",
		"-f",
		"{{.ImportPath}}",
		"./cmd/spice-bootstrap",
	)
	if err != nil {
		return commandError("audit compiler dependencies", dependencies, err)
	}
	return checkCompilerDependencies(dependencies)
}

func (p proof) proveProductionDogfood(ctx context.Context) error {
	output, err := p.runner(
		ctx,
		p.repositoryRoot,
		p.environment,
		p.spiceExecutable,
		append(
			[]string{"generate", "--check", "--target", "Spice"},
			productionPatterns()...,
		)...,
	)
	if err != nil {
		return commandError(
			"verify production graph with bootstrap compiler",
			output,
			err,
		)
	}
	output, err = p.runner(
		ctx,
		p.repositoryRoot,
		p.environment,
		"go",
		"build",
		"-mod=vendor",
		"-trimpath",
		"-o",
		p.productionExecutable,
		"./cmd/spice",
	)
	if err != nil {
		return commandError("build Spice production application", output, err)
	}
	dependencies, err := p.runner(
		ctx,
		p.repositoryRoot,
		p.environment,
		"go",
		"list",
		"-mod=vendor",
		"-deps",
		"-f",
		"{{.ImportPath}}",
		"./cmd/spice",
	)
	if err != nil {
		return commandError(
			"audit Spice production dependencies",
			dependencies,
			err,
		)
	}
	if dependencyErr := checkProductionDependencies(dependencies); dependencyErr != nil {
		return dependencyErr
	}
	output, err = p.runner(
		ctx,
		p.repositoryRoot,
		p.environment,
		p.productionExecutable,
		"version",
	)
	if err != nil {
		return commandError("run Spice production application", output, err)
	}
	if !bytes.HasPrefix(output, []byte("spice ")) {
		return fmt.Errorf(
			"bootstrap check: production application omitted version output: %s",
			strings.TrimSpace(string(output)),
		)
	}
	output, err = p.runner(
		ctx,
		p.repositoryRoot,
		p.environment,
		p.productionExecutable,
		append(
			[]string{"generate", "--check", "--target", "Spice"},
			productionPatterns()...,
		)...,
	)
	if err != nil {
		return commandError(
			"verify production graph with production application",
			output,
			err,
		)
	}
	return nil
}

func productionPatterns() []string {
	return []string{
		"./compiler/...",
		"./internal/devloop",
		"./internal/genfs",
		"./internal/lsp",
		"./internal/cli",
		"./internal/spiceapp",
		"./internal/autoconfigure",
	}
}

func (p proof) generate(
	ctx context.Context,
	arguments ...string,
) ([]byte, error) {
	return p.runner(
		ctx,
		p.fixtureRoot,
		p.environment,
		p.spiceExecutable,
		append([]string{"generate"}, arguments...)...,
	)
}

func (p proof) generateBaseline(
	ctx context.Context,
) (map[string][]byte, error) {
	output, err := p.generate(ctx, ".", "./component")
	if err != nil {
		return nil, commandError(
			"generate application from zero output",
			output,
			err,
		)
	}
	baseline, err := snapshotOwnedOutput(p.fixtureRoot)
	if err != nil {
		return nil, err
	}
	if err := validateOwnedOutput(baseline); err != nil {
		return nil, err
	}
	if err := buildAndRunApplication(
		ctx,
		p.runner,
		p.fixtureRoot,
		p.temporaryRoot,
		p.environment,
		p.executableSuffix,
	); err != nil {
		return nil, err
	}
	return baseline, nil
}

func (p proof) proveZeroOutputRegeneration(
	ctx context.Context,
	baseline map[string][]byte,
) error {
	if removeErr := removeOwnedOutput(p.fixtureRoot); removeErr != nil {
		return removeErr
	}
	output, err := p.generate(ctx, ".", "./component")
	if err != nil {
		return commandError("regenerate application from zero output", output, err)
	}
	regenerated, err := snapshotOwnedOutput(p.fixtureRoot)
	if err != nil {
		return err
	}
	if comparisonErr := compareSnapshots(baseline, regenerated); comparisonErr != nil {
		return fmt.Errorf(
			"bootstrap check: deterministic regeneration: %w",
			comparisonErr,
		)
	}
	return nil
}

func (p proof) proveManualEditRecovery(
	ctx context.Context,
	baseline map[string][]byte,
) error {
	generatedPath, err := firstGeneratedGoFile(baseline)
	if err != nil {
		return err
	}
	fixture, err := os.OpenRoot(p.fixtureRoot)
	if err != nil {
		return fmt.Errorf("bootstrap check: open fixture root: %w", err)
	}
	file, err := fixture.OpenFile(
		filepath.FromSlash(generatedPath),
		os.O_APPEND|os.O_WRONLY,
		0,
	)
	if err != nil {
		return fmt.Errorf(
			"bootstrap check: corrupt generated output: %w",
			errors.Join(err, fixture.Close()),
		)
	}
	if _, writeErr := file.WriteString("\n@broken\n"); writeErr != nil {
		err = errors.Join(writeErr, file.Close())
	} else {
		err = file.Close()
	}
	err = errors.Join(err, fixture.Close())
	if err != nil {
		return fmt.Errorf("bootstrap check: corrupt generated output: %w", err)
	}
	output, checkErr := p.generate(ctx, "--check", ".", "./component")
	if checkErr == nil {
		return errors.New(
			"bootstrap check: manual generated edit passed freshness check",
		)
	}
	if !bytes.Contains(output, []byte("modified after its manifest")) &&
		!bytes.Contains(output, []byte("manual-edit")) {
		return fmt.Errorf(
			"bootstrap check: manual edit diagnostic was not actionable: %s",
			strings.TrimSpace(string(output)),
		)
	}

	if removeErr := removeOwnedOutput(p.fixtureRoot); removeErr != nil {
		return removeErr
	}
	output, err = p.generate(ctx, ".", "./component")
	if err != nil {
		return commandError("recover generated output", output, err)
	}
	recovered, err := snapshotOwnedOutput(p.fixtureRoot)
	if err != nil {
		return err
	}
	if comparisonErr := compareSnapshots(baseline, recovered); comparisonErr != nil {
		return fmt.Errorf(
			"bootstrap check: recovered output: %w",
			comparisonErr,
		)
	}
	return buildAndRunApplication(
		ctx,
		p.runner,
		p.fixtureRoot,
		p.temporaryRoot,
		p.environment,
		p.executableSuffix,
	)
}

func buildAndRunApplication(
	ctx context.Context,
	runner commandRunner,
	fixtureRoot string,
	temporaryRoot string,
	environment []string,
	executableSuffix string,
) error {
	applicationExecutable := filepath.Join(
		temporaryRoot,
		"bootstrap-application"+executableSuffix,
	)
	output, err := runner(
		ctx,
		fixtureRoot,
		environment,
		"go",
		"build",
		"-trimpath",
		"-o",
		applicationExecutable,
		".",
	)
	if err != nil {
		return commandError("build generated application", output, err)
	}
	output, err = runner(
		ctx,
		fixtureRoot,
		environment,
		applicationExecutable,
		"-check",
	)
	if err != nil {
		return commandError("run generated application", output, err)
	}
	if !bytes.Contains(bytes.ToLower(output), []byte("ready")) {
		return fmt.Errorf(
			"bootstrap check: generated application omitted readiness output: %s",
			strings.TrimSpace(string(output)),
		)
	}
	return nil
}

func execute(
	ctx context.Context,
	directory string,
	environment []string,
	name string,
	arguments ...string,
) ([]byte, error) {
	if err := validateExecutable(name); err != nil {
		return nil, err
	}
	// #nosec G204 -- validateExecutable restricts this internal seam to Go and
	// the absolute, temporary proof binaries created by this package.
	command := exec.CommandContext(ctx, name, arguments...)
	command.Dir = directory
	command.Env = environment
	return command.CombinedOutput()
}

func validateExecutable(name string) error {
	if name == "go" {
		return nil
	}
	if !filepath.IsAbs(name) {
		return fmt.Errorf(
			"bootstrap check: executable path is not absolute: %q",
			name,
		)
	}
	base := strings.TrimSuffix(
		strings.ToLower(filepath.Base(name)),
		".exe",
	)
	if base != "spice-bootstrap" &&
		base != "spice-production" &&
		base != "bootstrap-application" {
		return fmt.Errorf(
			"bootstrap check: executable is not part of the proof: %q",
			name,
		)
	}
	return nil
}

func commandError(operation string, output []byte, err error) error {
	text := strings.TrimSpace(string(output))
	if text == "" {
		return fmt.Errorf("bootstrap check: %s: %w", operation, err)
	}
	return fmt.Errorf("bootstrap check: %s: %w\n%s", operation, err, text)
}

func checkCompilerDependencies(output []byte) error {
	for line := range strings.Lines(string(output)) {
		importPath := strings.TrimSpace(line)
		if strings.Contains(importPath, spiceModule+"/internal/spicegen") {
			return fmt.Errorf(
				"bootstrap check: compiler depends on generated application package %q",
				importPath,
			)
		}
	}
	return nil
}

func checkProductionDependencies(output []byte) error {
	const productionPackage = spiceModule + "/internal/spicegen/spice"
	foundProductionPackage := false
	for line := range strings.Lines(string(output)) {
		importPath := strings.TrimSpace(line)
		if importPath == productionPackage {
			foundProductionPackage = true
		}
		if strings.Contains(importPath, spiceModule+"/internal/spicegen/") &&
			importPath != productionPackage &&
			!strings.HasPrefix(importPath, productionPackage+"/") {
			return fmt.Errorf(
				"bootstrap check: production application depends on unexpected generated target %q",
				importPath,
			)
		}
	}
	if !foundProductionPackage {
		return fmt.Errorf(
			"bootstrap check: production application does not depend on generated target %q",
			productionPackage,
		)
	}
	return nil
}

func offlineEnvironment(environment []string) []string {
	overrides := []string{
		"GOPROXY=off",
		"GOSUMDB=off",
		"GOWORK=off",
	}
	result := append([]string(nil), environment...)
	for _, override := range overrides {
		key, _, _ := strings.Cut(override, "=")
		result = slices.DeleteFunc(result, func(item string) bool {
			existing, _, _ := strings.Cut(item, "=")
			return strings.EqualFold(existing, key)
		})
		result = append(result, override)
	}
	return result
}

func writeFixture(root, repositoryRoot string) error {
	files := map[string]string{
		"go.mod": fmt.Sprintf(`module example.com/bootstrap

go 1.26.0

toolchain go1.26.5

tool github.com/StevenBuglione/spice/cmd/spice-annotation-core

require github.com/StevenBuglione/spice v0.0.0

replace github.com/StevenBuglione/spice => %s
`, strconv.Quote(filepath.ToSlash(repositoryRoot))),
		"main.go": `package main

import (
	"os"

	spiceapp "example.com/bootstrap/internal/spicegen/bootstrap"
)

// @import { Application } from "github.com/StevenBuglione/spice/annotation/core"

// @Application
func main() {
	os.Exit(spiceapp.Main(os.Args[1:]))
}
`,
		"component/message.go": `// Package component owns the bootstrap proof bean.
package component

// @import { Bean } from "github.com/StevenBuglione/spice/annotation/core"

// @Bean
func Message() string {
	return "ready"
}
`,
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		return fmt.Errorf("bootstrap check: create fixture root: %w", err)
	}
	fixture, err := os.OpenRoot(root)
	if err != nil {
		return fmt.Errorf("bootstrap check: open fixture root: %w", err)
	}
	var writeErr error
	for relative, content := range files {
		relative = filepath.FromSlash(relative)
		if err := fixture.MkdirAll(filepath.Dir(relative), 0o750); err != nil {
			writeErr = fmt.Errorf(
				"bootstrap check: create fixture directory: %w",
				err,
			)
			break
		}
		if err := fixture.WriteFile(relative, []byte(content), 0o600); err != nil {
			writeErr = fmt.Errorf(
				"bootstrap check: write fixture %s: %w",
				relative,
				err,
			)
			break
		}
	}
	return errors.Join(writeErr, fixture.Close())
}

func snapshotOwnedOutput(root string) (map[string][]byte, error) {
	ownedRoot, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("bootstrap check: open owned root: %w", err)
	}
	snapshot, snapshotErr := snapshotOwnedRoot(ownedRoot)
	return snapshot, errors.Join(snapshotErr, ownedRoot.Close())
}

func snapshotOwnedRoot(ownedRoot *os.Root) (map[string][]byte, error) {
	snapshot := make(map[string][]byte)
	for _, relativeRoot := range []string{".spice", "internal/spicegen"} {
		err := fs.WalkDir(ownedRoot.FS(), relativeRoot, func(
			path string,
			entry fs.DirEntry,
			walkErr error,
		) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			content, err := fs.ReadFile(ownedRoot.FS(), path)
			if err != nil {
				return err
			}
			snapshot[filepath.ToSlash(path)] = content
			return nil
		})
		if err != nil {
			return nil, fmt.Errorf(
				"bootstrap check: snapshot %s: %w",
				relativeRoot,
				err,
			)
		}
	}
	return snapshot, nil
}

func validateOwnedOutput(snapshot map[string][]byte) error {
	var manifest, generatedGo bool
	for path, content := range snapshot {
		manifest = manifest || strings.HasSuffix(path, ".manifest.json")
		generatedGo = generatedGo ||
			strings.HasSuffix(path, ".go") &&
				bytes.Contains(
					content,
					[]byte("// Code generated by Spice. DO NOT EDIT."),
				)
	}
	if !manifest || !generatedGo {
		return fmt.Errorf(
			"bootstrap check: incomplete generated ownership: manifest=%t generated_go=%t",
			manifest,
			generatedGo,
		)
	}
	return nil
}

func compareSnapshots(left, right map[string][]byte) error {
	if len(left) != len(right) {
		return fmt.Errorf("file count = %d, want %d", len(right), len(left))
	}
	for path, leftContent := range left {
		rightContent, found := right[path]
		if !found {
			return fmt.Errorf("missing %s", path)
		}
		if !bytes.Equal(leftContent, rightContent) {
			return fmt.Errorf("content changed for %s", path)
		}
	}
	return nil
}

func firstGeneratedGoFile(
	snapshot map[string][]byte,
) (string, error) {
	paths := make([]string, 0, len(snapshot))
	for path := range snapshot {
		if strings.HasSuffix(path, ".go") {
			paths = append(paths, path)
		}
	}
	slices.Sort(paths)
	if len(paths) == 0 {
		return "", errors.New("bootstrap check: generated Go file is missing")
	}
	return paths[0], nil
}

func removeOwnedOutput(root string) error {
	ownedRoot, err := os.OpenRoot(root)
	if err != nil {
		return fmt.Errorf("bootstrap check: open owned root: %w", err)
	}
	var removeErr error
	for _, relative := range []string{".spice", "internal/spicegen"} {
		if err := ownedRoot.RemoveAll(filepath.FromSlash(relative)); err != nil {
			removeErr = fmt.Errorf(
				"bootstrap check: remove owned output %s: %w",
				relative,
				err,
			)
			break
		}
	}
	return errors.Join(removeErr, ownedRoot.Close())
}
