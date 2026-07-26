// Package cli implements the Spice command-line interface.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/StevenBuglione/spice/annotation"
	"github.com/StevenBuglione/spice/annotation/builtin"
	"github.com/StevenBuglione/spice/compiler/application"
	codegen "github.com/StevenBuglione/spice/compiler/generate"
	"github.com/StevenBuglione/spice/compiler/load"
	"github.com/StevenBuglione/spice/compiler/modulith"
	"github.com/StevenBuglione/spice/compiler/provider"
	"github.com/StevenBuglione/spice/compiler/resolve"
	"github.com/StevenBuglione/spice/compiler/scan"
	compilerstarter "github.com/StevenBuglione/spice/compiler/starter"
	"github.com/StevenBuglione/spice/compiler/validate"
	"github.com/StevenBuglione/spice/internal/genfs"
)

// Version is the development version reported by the Spice CLI.
const Version = "0.1.0-dev"

const (
	starterSelectionPath    = ".spice/starters.json"
	maxStarterSelectionSize = 4 << 20
)

type (
	programLoader func(context.Context, load.Options, ...string) (*load.Program, error)
	buildExecutor func(context.Context, string, io.Writer, io.Writer) error
	testExecutor  func(context.Context, string, []string, io.Writer, io.Writer) error
)

// Run executes Spice and returns a process exit code.
func Run(arguments []string, stdout, stderr io.Writer) int {
	return run(arguments, stdout, stderr, load.Options{}, load.Load)
}

func run(arguments []string, stdout, stderr io.Writer, options load.Options, loader programLoader) int {
	return runWithBuilder(arguments, stdout, stderr, options, loader, executeGoBuild)
}

func runWithBuilder(
	arguments []string,
	stdout io.Writer,
	stderr io.Writer,
	options load.Options,
	loader programLoader,
	builder buildExecutor,
) int {
	return runWithExecutors(
		arguments,
		stdout,
		stderr,
		options,
		loader,
		builder,
		executeGoTest,
	)
}

func runWithExecutors(
	arguments []string,
	stdout io.Writer,
	stderr io.Writer,
	options load.Options,
	loader programLoader,
	builder buildExecutor,
	tester testExecutor,
) int {
	if len(arguments) == 0 {
		return helpCommand(stdout)
	}

	switch arguments[0] {
	case "help", "-h", "--help":
		return helpCommand(stdout)
	case "version", "--version":
		return versionCommand(stdout)
	case "verify":
		return verify(packagePatterns(arguments[1:]), stdout, stderr, options, loader)
	case "annotations":
		return annotations(packagePatterns(arguments[1:]), stdout, stderr, options, loader)
	case "modules":
		return modulesCommand(arguments[1:], stdout, stderr, options, loader)
	case "test":
		return moduleTestCommand(arguments[1:], stdout, stderr, options, loader, tester)
	case "generate":
		return generateCommand(arguments[1:], stdout, stderr, options, loader)
	case "build":
		return buildCommand(arguments[1:], stdout, stderr, options, loader, builder)
	default:
		return unknownCommand(arguments[0], stderr)
	}
}

func helpCommand(stdout io.Writer) int {
	if err := printHelp(stdout); err != nil {
		return 1
	}
	return 0
}

func versionCommand(stdout io.Writer) int {
	if err := writef(stdout, "spice %s\n", Version); err != nil {
		return 1
	}
	return 0
}

func unknownCommand(name string, stderr io.Writer) int {
	if err := writef(stderr, "unknown command %q\n\n", name); err != nil {
		return 1
	}
	if err := printHelp(stderr); err != nil {
		return 1
	}
	return 2
}

type moduleTestArguments struct {
	module   string
	race     bool
	count    string
	run      string
	timeout  string
	patterns []string
}

func moduleTestCommand(
	arguments []string,
	stdout io.Writer,
	stderr io.Writer,
	options load.Options,
	loader programLoader,
	tester testExecutor,
) int {
	parsed, err := parseModuleTestArguments(arguments)
	if err != nil {
		if writeErr := writef(stderr, "Spice module test failed: %v\n", err); writeErr != nil {
			return 1
		}
		return 2
	}
	if len(parsed.patterns) == 0 {
		parsed.patterns = []string{"./..."}
	}
	focused, ok := prepareFocusedModuleTest(parsed, stderr, options, loader)
	if !ok {
		return 1
	}
	packages := focusedModulePackages(focused)
	if len(packages) == 0 {
		if writeErr := writef(
			stderr,
			"Spice module test failed: focused module %q has no owned packages\n",
			parsed.module,
		); writeErr != nil {
			return 1
		}
		return 1
	}
	directory := options.Dir
	if directory == "" {
		directory = "."
	}
	if err := tester(
		context.Background(),
		directory,
		parsed.goTestArguments(packages),
		stdout,
		stderr,
	); err != nil {
		if writeErr := writef(stderr, "Spice module test failed for %s: %v\n", parsed.module, err); writeErr != nil {
			return 1
		}
		return 1
	}
	if err := writef(
		stdout,
		"Spice module tests passed for %s: %d package(s) across %d module(s).\n",
		parsed.module,
		len(packages),
		len(focused.Modules()),
	); err != nil {
		return 1
	}
	return 0
}

func prepareFocusedModuleTest(
	arguments moduleTestArguments,
	stderr io.Writer,
	options load.Options,
	loader programLoader,
) (modulith.Model, bool) {
	program, resolution, _, ok := resolveValidatedCompilerPatterns(
		arguments.patterns,
		stderr,
		options,
		loader,
		"module test discovery",
		"Spice module test failed",
	)
	if !ok {
		return modulith.Model{}, false
	}
	model := modulith.Build(program, resolution)
	if diagnostics := model.Diagnostics(); len(diagnostics) != 0 {
		if reportErr := reportDiagnostics(
			stderr,
			diagnostics,
			fmt.Sprintf("Spice module test failed: %d module architecture error(s).", len(diagnostics)),
		); reportErr != nil {
			return modulith.Model{}, false
		}
		return modulith.Model{}, false
	}
	focused, err := model.Focus(arguments.module)
	if err != nil {
		if writeErr := writef(stderr, "Spice module test failed: %v\n", err); writeErr != nil {
			return modulith.Model{}, false
		}
		return modulith.Model{}, false
	}
	return focused, true
}

func parseModuleTestArguments(arguments []string) (moduleTestArguments, error) {
	var result moduleTestArguments
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		if !strings.HasPrefix(argument, "-") {
			result.patterns = append(result.patterns, argument)
			continue
		}
		next, err := parseModuleTestOption(arguments, index, &result)
		if err != nil {
			return moduleTestArguments{}, err
		}
		index = next
	}
	if result.module == "" {
		return moduleTestArguments{}, errors.New("--module requires a full module import path")
	}
	return result, nil
}

func parseModuleTestOption(
	arguments []string,
	index int,
	result *moduleTestArguments,
) (int, error) {
	argument := arguments[index]
	switch {
	case argument == "--module" || strings.HasPrefix(argument, "--module="):
		return setModuleTestOption(
			arguments,
			index,
			"--module",
			"a full module import path",
			&result.module,
			nil,
		)
	case argument == "--race":
		if result.race {
			return index, errors.New("--race may be specified only once")
		}
		result.race = true
		return index, nil
	case argument == "--count" || strings.HasPrefix(argument, "--count="):
		return setModuleTestOption(
			arguments,
			index,
			"--count",
			"a positive integer",
			&result.count,
			validateModuleTestCount,
		)
	case argument == "--run" || strings.HasPrefix(argument, "--run="):
		return setModuleTestOption(
			arguments,
			index,
			"--run",
			"a non-empty Go test expression",
			&result.run,
			nil,
		)
	case argument == "--timeout" || strings.HasPrefix(argument, "--timeout="):
		return setModuleTestOption(
			arguments,
			index,
			"--timeout",
			"a positive Go duration",
			&result.timeout,
			validateModuleTestTimeout,
		)
	default:
		return index, fmt.Errorf("unknown module test option %q", argument)
	}
}

func setModuleTestOption(
	arguments []string,
	index int,
	name string,
	expected string,
	target *string,
	validate func(string) error,
) (int, error) {
	value, next, err := moduleOptionValue(arguments, index, name, *target != "", expected)
	if err != nil {
		return index, err
	}
	if validate != nil {
		if err := validate(value); err != nil {
			return index, err
		}
	}
	*target = value
	return next, nil
}

func validateModuleTestCount(value string) error {
	count, err := strconv.Atoi(value)
	if err != nil || count <= 0 {
		return fmt.Errorf("--count requires a positive integer, got %q", value)
	}
	return nil
}

func validateModuleTestTimeout(value string) error {
	timeout, err := time.ParseDuration(value)
	if err != nil || timeout <= 0 {
		return fmt.Errorf("--timeout requires a positive Go duration, got %q", value)
	}
	return nil
}

func (arguments moduleTestArguments) goTestArguments(packages []string) []string {
	result := []string{"-trimpath"}
	if arguments.race {
		result = append(result, "-race")
	}
	if arguments.count != "" {
		result = append(result, "-count="+arguments.count)
	}
	if arguments.run != "" {
		result = append(result, "-run="+arguments.run)
	}
	if arguments.timeout != "" {
		result = append(result, "-timeout="+arguments.timeout)
	}
	return append(result, packages...)
}

func focusedModulePackages(model modulith.Model) []string {
	modules := make(map[string]modulith.Module, len(model.Modules()))
	for _, module := range model.Modules() {
		modules[module.ID] = module
	}
	var result []string
	for _, moduleID := range model.DependencyOrder() {
		module, ok := modules[moduleID]
		if !ok {
			continue
		}
		for _, pkg := range module.Packages() {
			result = append(result, pkg.Path)
		}
	}
	return result
}

type moduleArguments struct {
	format    modulith.Format
	formatSet bool
	focus     string
	patterns  []string
}

func modulesCommand(
	arguments []string,
	stdout io.Writer,
	stderr io.Writer,
	options load.Options,
	loader programLoader,
) int {
	parsed, parseErr := parseModuleArguments(arguments)
	if parseErr != nil {
		if writeErr := writef(stderr, "Spice module documentation failed: %v\n", parseErr); writeErr != nil {
			return 1
		}
		return 2
	}
	if len(parsed.patterns) == 0 {
		parsed.patterns = []string{"./..."}
	}
	program, resolution, _, ok := resolveValidatedCompilerPatterns(
		parsed.patterns,
		stderr,
		options,
		loader,
		"module discovery",
		"Spice module documentation failed",
	)
	if !ok {
		return 1
	}
	model := modulith.Build(program, resolution)
	if diagnostics := model.Diagnostics(); len(diagnostics) != 0 {
		if err := reportDiagnostics(
			stderr,
			diagnostics,
			fmt.Sprintf("Spice module documentation failed: %d module architecture error(s).", len(diagnostics)),
		); err != nil {
			return 1
		}
		return 1
	}
	if parsed.focus != "" {
		focused, focusErr := model.Focus(parsed.focus)
		if focusErr != nil {
			if writeErr := writef(stderr, "Spice module documentation failed: %v\n", focusErr); writeErr != nil {
				return 1
			}
			return 1
		}
		model = focused
	}
	content, err := modulith.Render(model, parsed.format)
	if err != nil {
		if writeErr := writef(stderr, "Spice module documentation failed: %v\n", err); writeErr != nil {
			return 1
		}
		return 1
	}
	if _, err := stdout.Write(content); err != nil {
		return 1
	}
	return 0
}

func parseModuleArguments(arguments []string) (moduleArguments, error) {
	result := moduleArguments{format: modulith.FormatJSON}
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		switch {
		case argument == "--format" || strings.HasPrefix(argument, "--format="):
			value, next, err := moduleOptionValue(
				arguments,
				index,
				"--format",
				result.formatSet,
				"json, mermaid, or plantuml",
			)
			if err != nil {
				return moduleArguments{}, err
			}
			index = next
			result.format = modulith.Format(value)
			result.formatSet = true
		case argument == "--focus" || strings.HasPrefix(argument, "--focus="):
			value, next, err := moduleOptionValue(
				arguments,
				index,
				"--focus",
				result.focus != "",
				"a full module import path",
			)
			if err != nil {
				return moduleArguments{}, err
			}
			index = next
			result.focus = value
		case strings.HasPrefix(argument, "-"):
			return moduleArguments{}, fmt.Errorf("unknown module option %q", argument)
		default:
			result.patterns = append(result.patterns, argument)
		}
	}
	switch result.format {
	case modulith.FormatJSON, modulith.FormatMermaid, modulith.FormatPlantUML:
		return result, nil
	default:
		return moduleArguments{}, fmt.Errorf(
			"unsupported module format %q; expected json, mermaid, or plantuml",
			result.format,
		)
	}
}

func moduleOptionValue(
	arguments []string,
	index int,
	name string,
	alreadySet bool,
	expected string,
) (string, int, error) {
	if alreadySet {
		return "", index, fmt.Errorf("%s may be specified only once", name)
	}
	argument := arguments[index]
	if argument == name {
		next := index + 1
		if next >= len(arguments) || strings.HasPrefix(arguments[next], "-") {
			return "", index, fmt.Errorf("%s requires %s", name, expected)
		}
		return arguments[next], next, nil
	}
	value := strings.TrimPrefix(argument, name+"=")
	if value == "" {
		return "", index, fmt.Errorf("%s requires %s", name, expected)
	}
	return value, index, nil
}

type generationArguments struct {
	check    bool
	diff     bool
	target   string
	patterns []string
}

func generateCommand(
	arguments []string,
	stdout io.Writer,
	stderr io.Writer,
	options load.Options,
	loader programLoader,
) int {
	parsed, err := parseGenerationArguments(arguments, true)
	if err != nil {
		if writeErr := writef(stderr, "Spice generation failed: %v\n", err); writeErr != nil {
			return 1
		}
		return 2
	}
	plan, targetName, ok := prepareGeneration(parsed, stderr, options, loader)
	if !ok {
		return 1
	}
	if parsed.check || parsed.diff {
		return checkGeneration(plan, targetName, parsed.diff, stdout, stderr)
	}
	result, err := genfs.Apply(plan)
	if err != nil {
		if writeErr := writef(stderr, "Spice generation failed for target %s: %v\n", targetName, err); writeErr != nil {
			return 1
		}
		return 1
	}
	if !result.Changed() {
		if err := writef(stdout, "Spice generation is current for target %s.\n", targetName); err != nil {
			return 1
		}
		return 0
	}
	if err := writef(
		stdout,
		"Spice generated target %s: wrote %d file(s), removed %d stale file(s), manifest updated=%t.\n",
		targetName,
		len(result.Written),
		len(result.Removed),
		result.ManifestUpdated,
	); err != nil {
		return 1
	}
	return 0
}

func buildCommand(
	arguments []string,
	stdout io.Writer,
	stderr io.Writer,
	options load.Options,
	loader programLoader,
	builder buildExecutor,
) int {
	parsed, err := parseGenerationArguments(arguments, false)
	if err != nil {
		if writeErr := writef(stderr, "Spice build failed: %v\n", err); writeErr != nil {
			return 1
		}
		return 2
	}
	plan, targetName, ok := prepareGeneration(parsed, stderr, options, loader)
	if !ok {
		return 1
	}
	result, err := genfs.Apply(plan)
	if err != nil {
		if writeErr := writef(stderr, "Spice build generation failed for target %s: %v\n", targetName, err); writeErr != nil {
			return 1
		}
		return 1
	}
	if result.Changed() {
		if err := writef(
			stdout,
			"Spice generated target %s: wrote %d file(s), removed %d stale file(s), manifest updated=%t.\n",
			targetName,
			len(result.Written),
			len(result.Removed),
			result.ManifestUpdated,
		); err != nil {
			return 1
		}
	}
	if err := builder(context.Background(), plan.Target().ModuleRoot, stdout, stderr); err != nil {
		if writeErr := writef(stderr, "Spice build failed for target %s: %v\n", targetName, err); writeErr != nil {
			return 1
		}
		return 1
	}
	if err := writef(stdout, "Spice build passed for target %s.\n", targetName); err != nil {
		return 1
	}
	return 0
}

func prepareGeneration(
	arguments generationArguments,
	stderr io.Writer,
	options load.Options,
	loader programLoader,
) (codegen.Plan, string, bool) {
	patterns := arguments.patterns
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}
	program, resolution, metadata, ok := resolveValidatedCompilerPatterns(
		patterns,
		stderr,
		withAnalysisBuildTag(options),
		loader,
		"generation",
		"Spice generation failed",
	)
	if !ok {
		return codegen.Plan{}, "", false
	}
	model := application.BuildWithOptions(program, resolution, metadata.buildOptions)
	if diagnostics := model.Diagnostics(); len(diagnostics) != 0 {
		if err := reportDiagnostics(
			stderr,
			diagnostics,
			strings.Replace(verificationSummary(diagnostics), "verification", "generation", 1),
		); err != nil {
			return codegen.Plan{}, "", false
		}
		return codegen.Plan{}, "", false
	}
	target, targetErr := selectApplicationTarget(model.Targets(), arguments.target)
	if targetErr != nil {
		if err := writef(stderr, "Spice generation failed: %v\n", targetErr); err != nil {
			return codegen.Plan{}, "", false
		}
		return codegen.Plan{}, "", false
	}
	generationTarget, diagnostics := codegen.DefaultTarget(program, target)
	if len(diagnostics) != 0 {
		if err := reportDiagnostics(
			stderr,
			diagnostics,
			fmt.Sprintf("Spice generation failed: %d target error(s).", len(diagnostics)),
		); err != nil {
			return codegen.Plan{}, "", false
		}
		return codegen.Plan{}, "", false
	}
	plan, diagnostics := codegen.Render(program, model, target, generationTarget)
	if len(diagnostics) != 0 {
		if err := reportDiagnostics(
			stderr,
			diagnostics,
			fmt.Sprintf("Spice generation failed: %d render error(s).", len(diagnostics)),
		); err != nil {
			return codegen.Plan{}, "", false
		}
		return codegen.Plan{}, "", false
	}
	return plan, target.Name, true
}

func checkGeneration(
	plan codegen.Plan,
	targetName string,
	withDiff bool,
	stdout io.Writer,
	stderr io.Writer,
) int {
	status, err := genfs.Check(plan)
	if err != nil {
		if writeErr := writef(stderr, "Spice generation check failed for target %s: %v\n", targetName, err); writeErr != nil {
			return 1
		}
		return 1
	}
	if status.Current {
		if err := writef(stdout, "Spice generation is current for target %s.\n", targetName); err != nil {
			return 1
		}
		return 0
	}
	for _, difference := range status.Differences {
		if err := writef(stderr, "%s: %s\n", difference.Path, difference.Message); err != nil {
			return 1
		}
	}
	if withDiff {
		diff, diffErr := genfs.Diff(plan)
		if diffErr != nil {
			if err := writef(stderr, "render generation diff: %v\n", diffErr); err != nil {
				return 1
			}
			return 1
		}
		if _, err := io.WriteString(stdout, diff); err != nil {
			return 1
		}
	}
	if err := writef(
		stderr,
		"Spice generation is stale for target %s: %d difference(s).\n",
		targetName,
		len(status.Differences),
	); err != nil {
		return 1
	}
	return 1
}

func parseGenerationArguments(arguments []string, allowReadOnly bool) (generationArguments, error) {
	result := generationArguments{}
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		switch {
		case argument == "--check":
			if !allowReadOnly {
				return generationArguments{}, errors.New("--check is supported by spice generate, not spice build")
			}
			result.check = true
		case argument == "--diff":
			if !allowReadOnly {
				return generationArguments{}, errors.New("--diff is supported by spice generate, not spice build")
			}
			result.diff = true
		case argument == "--target":
			index++
			if index >= len(arguments) || strings.HasPrefix(arguments[index], "--") {
				return generationArguments{}, errors.New("--target requires an application name")
			}
			if result.target != "" {
				return generationArguments{}, errors.New("--target may be specified only once")
			}
			result.target = arguments[index]
		case strings.HasPrefix(argument, "--target="):
			if result.target != "" {
				return generationArguments{}, errors.New("--target may be specified only once")
			}
			result.target = strings.TrimPrefix(argument, "--target=")
			if result.target == "" {
				return generationArguments{}, errors.New("--target requires an application name")
			}
		case strings.HasPrefix(argument, "-"):
			return generationArguments{}, fmt.Errorf("unknown generation option %q", argument)
		default:
			result.patterns = append(result.patterns, argument)
		}
	}
	return result, nil
}

func selectApplicationTarget(
	targets []application.Target,
	selector string,
) (application.Target, error) {
	if len(targets) == 0 {
		return application.Target{}, errors.New("no @Application marker was found in the selected packages")
	}
	if selector == "" {
		if len(targets) == 1 {
			return targets[0], nil
		}
		names := make([]string, len(targets))
		for index, target := range targets {
			names[index] = target.Name
		}
		return application.Target{}, fmt.Errorf(
			"multiple @Application targets were found (%s); select one with --target",
			strings.Join(names, ", "),
		)
	}
	var matches []application.Target
	for _, target := range targets {
		if target.Name == selector ||
			target.SymbolID == selector ||
			strings.EqualFold(target.Name, selector) {
			matches = append(matches, target)
		}
	}
	if len(matches) == 0 {
		return application.Target{}, fmt.Errorf("@Application target %q was not found", selector)
	}
	if len(matches) > 1 {
		return application.Target{}, fmt.Errorf(
			"@Application target %q is ambiguous; select by stable symbol ID",
			selector,
		)
	}
	return matches[0], nil
}

func executeGoBuild(
	ctx context.Context,
	directory string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	command := exec.CommandContext(ctx, "go", "build", "-trimpath", "./...")
	command.Dir = directory
	command.Env = os.Environ()
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("go build -trimpath ./...: %w", err)
	}
	return nil
}

func executeGoTest(
	ctx context.Context,
	directory string,
	arguments []string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	commandArguments := append([]string{"test"}, arguments...)
	// #nosec G204 -- arguments are validated flags or compiler-owned import paths passed directly to the fixed Go executable without a shell.
	command := exec.CommandContext(ctx, "go", commandArguments...)
	command.Dir = directory
	command.Env = os.Environ()
	command.Stdout = stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("go %s: %w", strings.Join(commandArguments, " "), err)
	}
	return nil
}

func verify(patterns []string, stdout, stderr io.Writer, options load.Options, loader programLoader) int {
	program, result, metadata, ok := resolveValidatedCompilerPatterns(
		patterns,
		stderr,
		options,
		loader,
		"verification",
		"Spice verification failed",
	)
	if !ok {
		return 1
	}
	model := application.BuildWithOptions(program, result, metadata.buildOptions)
	modelDiagnostics := model.Diagnostics()
	if len(modelDiagnostics) > 0 {
		if err := reportDiagnostics(
			stderr,
			modelDiagnostics,
			verificationSummary(modelDiagnostics),
		); err != nil {
			return 1
		}
		return 1
	}

	if err := writef(stdout, "Spice verification passed: %d annotations in %d Go files.\n", len(result.Occurrences), result.Files); err != nil {
		return 1
	}
	return 0
}

func verificationSummary(diagnostics []application.Diagnostic) string {
	label := "application model"
	if len(diagnostics) != 0 {
		switch diagnostics[0].Stage {
		case application.StageResolution:
			label = "annotation resolution"
		case application.StageProvider:
			label = "provider catalog"
		case application.StageGraph:
			label = "provider graph"
		case application.StageLifecycle:
			label = "lifecycle hook"
		case application.StageModule:
			label = "module architecture"
		case application.StageConfiguration:
			label = "configuration declaration"
		case application.StageController:
			label = "HTTP controller"
		case application.StageBootstrap:
			label = "application bootstrap"
		case application.StageApplication:
		}
	}
	return fmt.Sprintf("Spice verification failed: %d %s error(s).", len(diagnostics), label)
}

func annotations(patterns []string, stdout, stderr io.Writer, options load.Options, loader programLoader) int {
	_, result, ok := resolvePatterns(patterns, stderr, options, loader, "annotation resolution")
	if !ok {
		return 1
	}
	for _, occurrence := range result.Occurrences {
		path := filepath.ToSlash(occurrence.DisplayPosition.Filename)
		if err := writef(
			stdout,
			"%s:%d %s %s @%s\n",
			path,
			occurrence.DisplayPosition.Line,
			occurrence.Target,
			occurrence.Name,
			occurrence.Annotation.Name,
		); err != nil {
			return 1
		}
	}
	if err := writef(stdout, "Found %d annotations in %d Go files.\n", len(result.Occurrences), result.Files); err != nil {
		return 1
	}
	return 0
}

func resolvePatterns(patterns []string, stderr io.Writer, options load.Options, loader programLoader, operation string) (*load.Program, resolve.Result, bool) {
	program, err := loader(context.Background(), options, patterns...)
	if err != nil {
		if writeErr := writef(stderr, "Spice %s failed: %v\n", operation, err); writeErr != nil {
			return nil, resolve.Result{}, false
		}
		return nil, resolve.Result{}, false
	}
	result := resolve.Annotations(program)
	if len(result.Diagnostics) > 0 {
		if writeErr := reportDiagnostics(
			stderr,
			result.Diagnostics,
			fmt.Sprintf("Spice %s failed: %d annotation resolution error(s).", operation, len(result.Diagnostics)),
		); writeErr != nil {
			return program, result, false
		}
		return program, result, false
	}
	return program, result, true
}

func resolveValidatedCompilerPatterns(
	patterns []string,
	stderr io.Writer,
	options load.Options,
	loader programLoader,
	operation string,
	failurePrefix string,
) (*load.Program, resolve.Result, compilerMetadata, bool) {
	metadata, ok := prepareCompilerMetadata(stderr, options, operation)
	if !ok {
		return nil, resolve.Result{}, compilerMetadata{}, false
	}
	program, result, ok := resolvePatterns(
		patterns,
		stderr,
		metadata.loadOptions(options),
		loader,
		operation,
	)
	if !ok {
		return program, result, compilerMetadata{}, false
	}
	diagnostics := validationDiagnostics(result.Occurrences, metadata.registry)
	if len(diagnostics) != 0 {
		if err := reportDiagnostics(
			stderr,
			diagnostics,
			fmt.Sprintf("%s: %d annotation validation error(s).", failurePrefix, len(diagnostics)),
		); err != nil {
			return program, result, compilerMetadata{}, false
		}
		return program, result, compilerMetadata{}, false
	}

	requirements := metadata.starterCatalog.ActiveDependencies(
		result.Occurrences,
	)
	if len(requirements) != 0 {
		modules, err := loadModuleVersions(context.Background(), options)
		if err != nil {
			if writeErr := writef(
				stderr,
				"%s: inspect selected starter dependencies: %v\n",
				failurePrefix,
				err,
			); writeErr != nil {
				return program, result, compilerMetadata{}, false
			}
			return program, result, compilerMetadata{}, false
		}
		dependencyDiagnostics := metadata.starterCatalog.
			ValidateActiveModuleVersions(result.Occurrences, modules)
		if len(dependencyDiagnostics) != 0 {
			if err := reportDiagnostics(
				stderr,
				dependencyDiagnostics,
				fmt.Sprintf(
					"%s: %d starter dependency alignment error(s).",
					failurePrefix,
					len(dependencyDiagnostics),
				),
			); err != nil {
				return program, result, compilerMetadata{}, false
			}
			return program, result, compilerMetadata{}, false
		}
	}

	entryPoints := metadata.starterCatalog.ProviderEntrypoints(result.Occurrences)
	if len(entryPoints) == 0 {
		return program, result, metadata, true
	}
	catalog := provider.BuildEntrypoints(program, entryPoints)
	if diagnostics := catalog.Diagnostics(); len(diagnostics) != 0 {
		if err := reportDiagnostics(
			stderr,
			diagnostics,
			fmt.Sprintf("%s: %d starter entrypoint error(s).", failurePrefix, len(diagnostics)),
		); err != nil {
			return program, result, compilerMetadata{}, false
		}
		return program, result, compilerMetadata{}, false
	}
	metadata.buildOptions.ProviderCatalogs = append(
		append([]provider.Catalog(nil), metadata.buildOptions.ProviderCatalogs...),
		catalog,
	)
	return program, result, metadata, true
}

func withAnalysisBuildTag(options load.Options) load.Options {
	result := options
	result.BuildFlags = nil
	tags := map[string]struct{}{codegen.AnalysisBuildTag: {}}
	addTagsFromFlags(tags, goFlags(options.Env))
	for index := 0; index < len(options.BuildFlags); index++ {
		flag := options.BuildFlags[index]
		switch {
		case flag == "-tags" && index+1 < len(options.BuildFlags):
			index++
			addTagValue(tags, options.BuildFlags[index])
		case strings.HasPrefix(flag, "-tags="):
			addTagValue(tags, strings.TrimPrefix(flag, "-tags="))
		default:
			result.BuildFlags = append(result.BuildFlags, flag)
		}
	}
	ordered := make([]string, 0, len(tags))
	for tag := range tags {
		ordered = append(ordered, tag)
	}
	sort.Strings(ordered)
	result.BuildFlags = append(result.BuildFlags, "-tags="+strings.Join(ordered, ","))
	return result
}

func goFlags(environment []string) string {
	if environment == nil {
		return os.Getenv("GOFLAGS")
	}
	for _, value := range environment {
		name, flagValue, found := strings.Cut(value, "=")
		if found && strings.EqualFold(name, "GOFLAGS") {
			return flagValue
		}
	}
	return ""
}

func addTagsFromFlags(tags map[string]struct{}, flags string) {
	fields := strings.Fields(flags)
	for index := 0; index < len(fields); index++ {
		switch {
		case fields[index] == "-tags" && index+1 < len(fields):
			index++
			addTagValue(tags, fields[index])
		case strings.HasPrefix(fields[index], "-tags="):
			addTagValue(tags, strings.TrimPrefix(fields[index], "-tags="))
		}
	}
}

func addTagValue(tags map[string]struct{}, value string) {
	for tag := range strings.FieldsFuncSeq(value, func(character rune) bool {
		return character == ',' || character == ' '
	}) {
		if tag != "" {
			tags[tag] = struct{}{}
		}
	}
}

type compilerMetadata struct {
	registry       annotation.Registry
	starterCatalog compilerstarter.Catalog
	buildOptions   application.BuildOptions
}

func (metadata compilerMetadata) loadOptions(options load.Options) load.Options {
	result := options
	result.AuxiliaryPackages = append(
		append([]string(nil), options.AuxiliaryPackages...),
		metadata.starterCatalog.EntryPointPackages()...,
	)
	return result
}

func prepareCompilerMetadata(
	stderr io.Writer,
	options load.Options,
	operation string,
) (compilerMetadata, bool) {
	metadata, err := loadCompilerMetadata(options)
	if err == nil {
		return metadata, true
	}
	if writeErr := writef(stderr, "Spice %s failed: %v\n", operation, err); writeErr != nil {
		return compilerMetadata{}, false
	}
	return compilerMetadata{}, false
}

func loadCompilerMetadata(options load.Options) (compilerMetadata, error) {
	result := compilerMetadata{registry: builtin.Registry()}
	directory := options.Dir
	if directory == "" {
		directory = "."
	}
	content, found, err := readStarterSelection(directory)
	if err != nil {
		return compilerMetadata{}, err
	}
	if !found {
		return result, nil
	}

	catalog, err := compilerstarter.Parse(content)
	if err != nil {
		return compilerMetadata{}, fmt.Errorf(
			"parse starter selection %s: %w",
			starterSelectionPath,
			err,
		)
	}
	registry, err := catalog.Registry(result.registry)
	if err != nil {
		return compilerMetadata{}, fmt.Errorf(
			"compose starter selection %s: %w",
			starterSelectionPath,
			err,
		)
	}
	result.registry = registry
	result.starterCatalog = catalog
	result.buildOptions.BootstrapDefinitions = catalog.BootstrapDefinitions()
	return result, nil
}

func readStarterSelection(
	directory string,
) (content []byte, found bool, returnErr error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, false, fmt.Errorf("open starter selection root: %w", err)
	}
	defer func() {
		if closeErr := root.Close(); closeErr != nil {
			returnErr = errors.Join(
				returnErr,
				fmt.Errorf("close starter selection root: %w", closeErr),
			)
		}
	}()

	relativePath := filepath.FromSlash(starterSelectionPath)
	info, err := root.Lstat(relativePath)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf(
			"inspect starter selection %s: %w",
			starterSelectionPath,
			err,
		)
	}
	if !info.Mode().IsRegular() {
		return nil, false, fmt.Errorf(
			"starter selection %s must be a regular file",
			starterSelectionPath,
		)
	}
	if info.Size() > maxStarterSelectionSize {
		return nil, false, fmt.Errorf(
			"starter selection %s exceeds the %d-byte limit",
			starterSelectionPath,
			maxStarterSelectionSize,
		)
	}
	content, err = root.ReadFile(relativePath)
	if err != nil {
		return nil, false, fmt.Errorf(
			"read starter selection %s: %w",
			starterSelectionPath,
			err,
		)
	}
	if len(content) > maxStarterSelectionSize {
		return nil, false, fmt.Errorf(
			"starter selection %s exceeds the %d-byte limit",
			starterSelectionPath,
			maxStarterSelectionSize,
		)
	}
	return content, true, nil
}

func validationDiagnostics(
	occurrences []resolve.Occurrence,
	registry annotation.Registry,
) []validate.Diagnostic {
	diagnostics := make([]validate.Diagnostic, 0)
	for _, occurrence := range occurrences {
		diagnostics = append(diagnostics, validate.Occurrences([]scan.Occurrence{{
			Annotation: occurrence.Annotation,
			Target:     occurrence.Target,
			Name:       occurrence.Name,
			File:       occurrence.PhysicalFile,
		}}, registry)...)
	}
	return diagnostics
}

func packagePatterns(arguments []string) []string {
	if len(arguments) == 0 {
		return []string{"."}
	}
	return append([]string(nil), arguments...)
}

func printHelp(writer io.Writer) error {
	_, err := fmt.Fprintln(writer, `Spice Framework for Go

Usage:
  spice version
  spice verify [package-pattern ...]
  spice annotations [package-pattern ...]
  spice modules [--format json|mermaid|plantuml] [--focus module] [package-pattern ...]
  spice test --module module [--race] [--count n] [--run regexp] [--timeout duration] [package-pattern ...]
  spice generate [--target name] [--check] [--diff] [package-pattern ...]
  spice build [--target name] [package-pattern ...]

Commands:
  version      Print the Spice version.
  verify       Load, resolve, and validate Spice annotations for Go packages.
  annotations  List annotations and their exact typed declarations.
  modules      Validate and render application-module documentation.
  test         Validate and run one focused application-module test graph.
  generate     Render and safely apply or check generated application code.
  build        Generate an application and run the standard trimpath build.

Starter selection:
  Commit .spice/starters.json to explicitly compose compatible third-party
  annotations and constructor entrypoints. Installed or imported modules are
  never auto-enabled.`)
	return err
}

func writef(writer io.Writer, format string, arguments ...any) error {
	_, err := fmt.Fprintf(writer, format, arguments...)
	return err
}

func reportDiagnostics[T interface{ Error() string }](writer io.Writer, diagnostics []T, summary string) error {
	for _, diagnostic := range diagnostics {
		if _, err := fmt.Fprintln(writer, diagnostic.Error()); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(writer, summary)
	return err
}
