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
	"strings"

	"github.com/StevenBuglione/spice/annotation/builtin"
	"github.com/StevenBuglione/spice/compiler/application"
	codegen "github.com/StevenBuglione/spice/compiler/generate"
	"github.com/StevenBuglione/spice/compiler/load"
	"github.com/StevenBuglione/spice/compiler/resolve"
	"github.com/StevenBuglione/spice/compiler/scan"
	"github.com/StevenBuglione/spice/compiler/validate"
	"github.com/StevenBuglione/spice/internal/genfs"
)

// Version is the development version reported by the Spice CLI.
const Version = "0.1.0-dev"

type (
	programLoader func(context.Context, load.Options, ...string) (*load.Program, error)
	buildExecutor func(context.Context, string, io.Writer, io.Writer) error
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
	if len(arguments) == 0 {
		if err := printHelp(stdout); err != nil {
			return 1
		}
		return 0
	}

	switch arguments[0] {
	case "help", "-h", "--help":
		if err := printHelp(stdout); err != nil {
			return 1
		}
		return 0
	case "version", "--version":
		if err := writef(stdout, "spice %s\n", Version); err != nil {
			return 1
		}
		return 0
	case "verify":
		return verify(packagePatterns(arguments[1:]), stdout, stderr, options, loader)
	case "annotations":
		return annotations(packagePatterns(arguments[1:]), stdout, stderr, options, loader)
	case "generate":
		return generateCommand(arguments[1:], stdout, stderr, options, loader)
	case "build":
		return buildCommand(arguments[1:], stdout, stderr, options, loader, builder)
	default:
		if err := writef(stderr, "unknown command %q\n\n", arguments[0]); err != nil {
			return 1
		}
		if err := printHelp(stderr); err != nil {
			return 1
		}
		return 2
	}
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
	program, resolution, ok := resolvePatterns(
		patterns,
		stderr,
		withAnalysisBuildTag(options),
		loader,
		"generation",
	)
	if !ok {
		return codegen.Plan{}, "", false
	}
	if diagnostics := validationDiagnostics(resolution.Occurrences); len(diagnostics) != 0 {
		if err := reportDiagnostics(
			stderr,
			diagnostics,
			fmt.Sprintf("Spice generation failed: %d annotation validation error(s).", len(diagnostics)),
		); err != nil {
			return codegen.Plan{}, "", false
		}
		return codegen.Plan{}, "", false
	}
	model := application.Build(program, resolution)
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

func verify(patterns []string, stdout, stderr io.Writer, options load.Options, loader programLoader) int {
	program, result, ok := resolvePatterns(patterns, stderr, options, loader, "verification")
	if !ok {
		return 1
	}

	diagnostics := validationDiagnostics(result.Occurrences)
	if len(diagnostics) > 0 {
		if err := reportDiagnostics(
			stderr,
			diagnostics,
			fmt.Sprintf("Spice verification failed: %d annotation validation error(s).", len(diagnostics)),
		); err != nil {
			return 1
		}
		return 1
	}
	model := application.Build(program, result)
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

func validationDiagnostics(occurrences []resolve.Occurrence) []validate.Diagnostic {
	diagnostics := make([]validate.Diagnostic, 0)
	registry := builtin.Registry()
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
  spice generate [--target name] [--check] [--diff] [package-pattern ...]
  spice build [--target name] [package-pattern ...]

Commands:
  version      Print the Spice version.
  verify       Load, resolve, and validate Spice annotations for Go packages.
  annotations  List annotations and their exact typed declarations.
  generate     Render and safely apply or check generated application code.
  build        Generate an application and run the standard trimpath build.`)
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
