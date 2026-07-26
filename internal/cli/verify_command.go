package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/StevenBuglione/spice/compiler/application"
	"github.com/StevenBuglione/spice/compiler/diagnostic"
	diagnosticadapt "github.com/StevenBuglione/spice/compiler/diagnostic/adapt"
	"github.com/StevenBuglione/spice/compiler/load"
	"github.com/StevenBuglione/spice/compiler/provider"
	"github.com/StevenBuglione/spice/compiler/resolve"
)

type diagnosticFormat string

const (
	diagnosticFormatText diagnosticFormat = "text"
	diagnosticFormatJSON diagnosticFormat = "json"
)

type verifyArguments struct {
	format    diagnosticFormat
	formatSet bool
	patterns  []string
}

type verificationAnalysis struct {
	program    *load.Program
	resolution resolve.Result
	metadata   compilerMetadata
}

type verificationFailure struct {
	diagnostics diagnostic.Set
	summary     string
}

func verifyCommand(
	arguments []string,
	stdout io.Writer,
	stderr io.Writer,
	options load.Options,
	loader programLoader,
) int {
	parsed, err := parseVerifyArguments(arguments)
	if err != nil {
		if writeErr := writef(stderr, "Spice verification failed: %v\n", err); writeErr != nil {
			return 1
		}
		return 2
	}
	return verifyPrepared(parsed, stdout, stderr, options, loader)
}

func verifyPrepared(
	arguments verifyArguments,
	stdout io.Writer,
	stderr io.Writer,
	options load.Options,
	loader programLoader,
) int {
	analysis, failure := analyzeVerification(
		arguments.patterns,
		options,
		loader,
	)
	if failure != nil {
		return reportVerification(
			arguments.format,
			false,
			failure.summary,
			failure.diagnostics,
			stdout,
			stderr,
		)
	}
	model := application.BuildWithOptions(
		analysis.program,
		analysis.resolution,
		analysis.metadata.buildOptions,
	)
	modelDiagnostics := model.Diagnostics()
	if len(modelDiagnostics) != 0 {
		return reportVerification(
			arguments.format,
			false,
			verificationSummary(modelDiagnostics),
			diagnosticadapt.Application(
				diagnosticWorkspaceRoot(options),
				modelDiagnostics,
			),
			stdout,
			stderr,
		)
	}

	summary := fmt.Sprintf(
		"Spice verification passed: %d annotations in %d Go files.",
		len(analysis.resolution.Occurrences),
		analysis.resolution.Files,
	)
	return reportVerification(
		arguments.format,
		true,
		summary,
		diagnostic.NewSet(),
		stdout,
		stderr,
	)
}

func parseVerifyArguments(arguments []string) (verifyArguments, error) {
	result := verifyArguments{format: diagnosticFormatText}
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		switch {
		case argument == "--format" ||
			strings.HasPrefix(argument, "--format="):
			value, next, err := moduleOptionValue(
				arguments,
				index,
				"--format",
				result.formatSet,
				"text or json",
			)
			if err != nil {
				return verifyArguments{}, err
			}
			index = next
			result.format = diagnosticFormat(value)
			result.formatSet = true
		case strings.HasPrefix(argument, "-"):
			return verifyArguments{}, fmt.Errorf(
				"unknown verification option %q",
				argument,
			)
		default:
			result.patterns = append(result.patterns, argument)
		}
	}
	switch result.format {
	case diagnosticFormatText, diagnosticFormatJSON:
	default:
		return verifyArguments{}, fmt.Errorf(
			"unsupported diagnostic format %q; expected text or json",
			result.format,
		)
	}
	if len(result.patterns) == 0 {
		result.patterns = []string{"./..."}
	}
	return result, nil
}

func analyzeVerification(
	patterns []string,
	options load.Options,
	loader programLoader,
) (verificationAnalysis, *verificationFailure) {
	metadata, err := loadCompilerMetadata(options)
	if err != nil {
		return verificationAnalysis{}, verificationProblem(
			diagnosticadapt.Failure("metadata", "invalid", err.Error()),
			"Spice verification failed: compiler metadata error.",
		)
	}
	program, err := loader(
		context.Background(),
		metadata.loadOptions(options),
		patterns...,
	)
	if err != nil {
		return verificationAnalysis{}, loadVerificationProblem(
			err,
			options,
		)
	}
	resolution := resolve.Annotations(program)
	if len(resolution.Diagnostics) != 0 {
		return verificationAnalysis{}, verificationProblem(
			diagnosticadapt.Resolution(
				diagnosticWorkspaceRoot(options),
				resolution.Diagnostics,
			),
			fmt.Sprintf(
				"Spice verification failed: %d annotation resolution error(s).",
				len(resolution.Diagnostics),
			),
		)
	}
	validation := validationDiagnostics(
		resolution.Occurrences,
		metadata.registry,
	)
	if len(validation) != 0 {
		return verificationAnalysis{}, verificationProblem(
			diagnosticadapt.Validation(
				diagnosticWorkspaceRoot(options),
				validation,
			),
			fmt.Sprintf(
				"Spice verification failed: %d annotation validation error(s).",
				len(validation),
			),
		)
	}
	if failure := validateVerificationStarters(
		options,
		program,
		resolution,
		&metadata,
	); failure != nil {
		return verificationAnalysis{}, failure
	}
	return verificationAnalysis{
		program:    program,
		resolution: resolution,
		metadata:   metadata,
	}, nil
}

func validateVerificationStarters(
	options load.Options,
	program *load.Program,
	resolution resolve.Result,
	metadata *compilerMetadata,
) *verificationFailure {
	requirements := metadata.starterCatalog.ActiveDependencies(
		resolution.Occurrences,
	)
	if len(requirements) != 0 {
		modules, err := loadModuleVersions(context.Background(), options)
		if err != nil {
			return verificationProblem(
				diagnosticadapt.Failure(
					"starter",
					"dependency-inspection",
					fmt.Sprintf(
						"inspect selected starter dependencies: %v",
						err,
					),
				),
				"Spice verification failed: starter dependency inspection error.",
			)
		}
		dependencyDiagnostics := metadata.starterCatalog.
			ValidateActiveModuleVersions(resolution.Occurrences, modules)
		if len(dependencyDiagnostics) != 0 {
			return verificationProblem(
				diagnosticadapt.StarterDependencies(
					dependencyDiagnostics,
				),
				fmt.Sprintf(
					"Spice verification failed: %d starter dependency alignment error(s).",
					len(dependencyDiagnostics),
				),
			)
		}
	}

	entryPoints := metadata.starterCatalog.ProviderEntrypoints(
		resolution.Occurrences,
	)
	if len(entryPoints) == 0 {
		return nil
	}
	catalog := provider.BuildEntrypoints(program, entryPoints)
	if diagnostics := catalog.Diagnostics(); len(diagnostics) != 0 {
		return verificationProblem(
			diagnosticadapt.Provider(
				diagnosticWorkspaceRoot(options),
				diagnostics,
			),
			fmt.Sprintf(
				"Spice verification failed: %d starter entrypoint error(s).",
				len(diagnostics),
			),
		)
	}
	metadata.buildOptions.ProviderCatalogs = append(
		append(
			[]provider.Catalog(nil),
			metadata.buildOptions.ProviderCatalogs...,
		),
		catalog,
	)
	return nil
}

func loadVerificationProblem(
	err error,
	options load.Options,
) *verificationFailure {
	loadError, ok := errors.AsType[*load.LoadError](err)
	if ok && loadError != nil && len(loadError.Diagnostics) != 0 {
		return verificationProblem(
			diagnosticadapt.Load(
				diagnosticWorkspaceRoot(options),
				loadError.Diagnostics,
			),
			fmt.Sprintf(
				"Spice verification failed: %d package loading error(s).",
				len(loadError.Diagnostics),
			),
		)
	}
	kind := "failed"
	if errors.Is(err, context.Canceled) {
		kind = "canceled"
	}
	return verificationProblem(
		diagnosticadapt.Failure("load", kind, err.Error()),
		"Spice verification failed: package loading error.",
	)
}

func verificationProblem(
	diagnostics diagnostic.Set,
	summary string,
) *verificationFailure {
	return &verificationFailure{
		diagnostics: diagnostics,
		summary:     summary,
	}
}

func reportVerification(
	format diagnosticFormat,
	success bool,
	summary string,
	diagnostics diagnostic.Set,
	stdout io.Writer,
	stderr io.Writer,
) int {
	if format == diagnosticFormatJSON {
		content, err := diagnostic.NewReport(
			success,
			summary,
			diagnostics,
		).JSON()
		if err != nil {
			if writeErr := writef(stderr, "Spice verification failed: %v\n", err); writeErr != nil {
				return 1
			}
			return 1
		}
		if _, err := stdout.Write(content); err != nil {
			return 1
		}
		if success {
			return 0
		}
		return 1
	}
	if success {
		if err := writef(stdout, "%s\n", summary); err != nil {
			return 1
		}
		return 0
	}
	for _, item := range diagnostics.Items() {
		if _, err := fmt.Fprintln(stderr, item.Error()); err != nil {
			return 1
		}
	}
	if _, err := fmt.Fprintln(stderr, summary); err != nil {
		return 1
	}
	return 1
}

func diagnosticWorkspaceRoot(options load.Options) string {
	if options.Dir == "" {
		return "."
	}
	return options.Dir
}
