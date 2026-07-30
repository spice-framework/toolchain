package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/StevenBuglione/spice/compiler/application"
	"github.com/StevenBuglione/spice/compiler/diagnostic"
	codegen "github.com/StevenBuglione/spice/compiler/generate"
	"github.com/StevenBuglione/spice/compiler/load"
	compilerservice "github.com/StevenBuglione/spice/compiler/service"
	"github.com/StevenBuglione/spice/internal/genfs"
)

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
	return prepareGenerationContext(
		context.Background(),
		arguments,
		stderr,
		options,
		loader,
	)
}

func prepareGenerationContext(
	ctx context.Context,
	arguments generationArguments,
	stderr io.Writer,
	options load.Options,
	loader programLoader,
) (codegen.Plan, string, bool) {
	service, err := newCompilerAnalysisService(options, loader)
	if err != nil {
		if writeErr := writef(stderr, "Spice generation failed: %v\n", err); writeErr != nil {
			return codegen.Plan{}, "", false
		}
		return codegen.Plan{}, "", false
	}
	root := options.Dir
	if root == "" {
		root = "."
	}
	plan, target, ready := prepareGenerationAnalysis(
		ctx,
		arguments,
		stderr,
		root,
		service,
		0,
	)
	if closeErr := closeCompilerAnalysisService(service); closeErr != nil {
		if writeErr := writef(
			stderr,
			"Spice generation failed: close annotation tools: %v\n",
			closeErr,
		); writeErr != nil {
			return codegen.Plan{}, "", false
		}
		return codegen.Plan{}, "", false
	}
	return plan, target, ready
}

func prepareGenerationAnalysis(
	ctx context.Context,
	arguments generationArguments,
	stderr io.Writer,
	root string,
	service *compilerservice.Service,
	sequence uint64,
) (codegen.Plan, string, bool) {
	patterns := arguments.patterns
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}
	result, err := service.Analyze(
		ctx,
		compilerservice.Request{
			WorkspaceRoot: root,
			Target:        arguments.target,
			Patterns:      patterns,
			Sequence:      sequence,
		},
	)
	if err != nil {
		if ctx.Err() != nil {
			return codegen.Plan{}, "", false
		}
		if writeErr := writef(stderr, "Spice generation failed: %v\n", err); writeErr != nil {
			return codegen.Plan{}, "", false
		}
		return codegen.Plan{}, "", false
	}
	if diagnostics := result.Diagnostics().Items(); len(diagnostics) != 0 {
		if err := reportDiagnostics(
			stderr,
			diagnostics,
			generationDiagnosticSummary(diagnostics),
		); err != nil {
			return codegen.Plan{}, "", false
		}
		return codegen.Plan{}, "", false
	}
	plan, found := result.GenerationPlan()
	if !found {
		if err := writef(
			stderr,
			"Spice generation failed: compiler analysis produced no generation plan.\n",
		); err != nil {
			return codegen.Plan{}, "", false
		}
		return codegen.Plan{}, "", false
	}
	return plan, result.TargetName(), true
}

func newCompilerAnalysisService(
	options load.Options,
	loader programLoader,
) (*compilerservice.Service, error) {
	if err := rejectLegacyStarterSelection(options); err != nil {
		return nil, err
	}
	return compilerservice.New(compilerservice.Config{
		Loader:         compilerservice.Loader(loader),
		ModuleVersions: loadModuleVersions,
		LoadOptions:    options,
		SpiceVersion:   Version,
	})
}

func closeCompilerAnalysisService(
	service *compilerservice.Service,
) error {
	ctx, cancel := context.WithTimeout(
		context.Background(),
		10*time.Second,
	)
	defer cancel()
	return service.Close(ctx)
}

func generationDiagnosticSummary(
	diagnostics []diagnostic.Diagnostic,
) string {
	stage := "compiler"
	if len(diagnostics) != 0 {
		segments := strings.Split(diagnostics[0].Code, ".")
		if len(segments) >= 3 {
			stage = segments[1]
		}
	}
	description := map[string]string{
		"load":        "package loading",
		"resolution":  "annotation resolution",
		"validation":  "annotation validation",
		"starter":     "starter dependency alignment",
		"provider":    "provider catalog",
		"application": "application model",
		"modulith":    "module architecture",
		"generation":  "generation",
	}[stage]
	if description == "" {
		description = "compiler"
	}
	return fmt.Sprintf(
		"Spice generation failed: %d %s error(s).",
		len(diagnostics),
		description,
	)
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
			target.PackagePath == selector ||
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

func verificationSummary(diagnostics []application.Diagnostic) string {
	label := "application model"
	if len(diagnostics) != 0 {
		label = verificationStageLabel(diagnostics[0].Stage)
	}
	return fmt.Sprintf("Spice verification failed: %d %s error(s).", len(diagnostics), label)
}

func verificationStageLabel(stage application.Stage) string {
	labels := map[application.Stage]string{
		application.StageResolution:    "annotation resolution",
		application.StageProvider:      "provider catalog",
		application.StageGraph:         "provider graph",
		application.StageLifecycle:     "lifecycle hook",
		application.StageSchedule:      "scheduled method",
		application.StageAsync:         "asynchronous method",
		application.StageTransaction:   "transaction boundary",
		application.StageCache:         "cache boundary",
		application.StageEvent:         "event contract",
		application.StageModule:        "module architecture",
		application.StageConfiguration: "configuration declaration",
		application.StageController:    "HTTP controller",
		application.StageBootstrap:     "application bootstrap",
		application.StageApplication:   "application model",
	}
	if label, found := labels[stage]; found {
		return label
	}
	return "application model"
}
