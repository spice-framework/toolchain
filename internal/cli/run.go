// Package cli implements the Spice command-line interface.
package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/StevenBuglione/spice/annotation/builtin"
	"github.com/StevenBuglione/spice/compiler/application"
	"github.com/StevenBuglione/spice/compiler/load"
	"github.com/StevenBuglione/spice/compiler/resolve"
	"github.com/StevenBuglione/spice/compiler/scan"
	"github.com/StevenBuglione/spice/compiler/validate"
)

// Version is the development version reported by the Spice CLI.
const Version = "0.1.0-dev"

type programLoader func(context.Context, load.Options, ...string) (*load.Program, error)

// Run executes Spice and returns a process exit code.
func Run(arguments []string, stdout, stderr io.Writer) int {
	return run(arguments, stdout, stderr, load.Options{}, load.Load)
}

func run(arguments []string, stdout, stderr io.Writer, options load.Options, loader programLoader) int {
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

Commands:
  version      Print the Spice version.
  verify       Load, resolve, and validate Spice annotations for Go packages.
  annotations  List annotations and their exact typed declarations.`)
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
