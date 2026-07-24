// Package cli implements the Spice command-line interface.
package cli

import (
	"context"
	"fmt"
	"io"
	"path/filepath"

	"github.com/StevenBuglione/spice/annotation/builtin"
	"github.com/StevenBuglione/spice/compiler/load"
	"github.com/StevenBuglione/spice/compiler/resolve"
	"github.com/StevenBuglione/spice/compiler/scan"
	"github.com/StevenBuglione/spice/compiler/validate"
)

const Version = "0.1.0-dev"

type programLoader func(context.Context, load.Options, ...string) (*load.Program, error)

// Run executes Spice and returns a process exit code.
func Run(arguments []string, stdout, stderr io.Writer) int {
	return run(arguments, stdout, stderr, load.Options{}, load.Load)
}

func run(arguments []string, stdout, stderr io.Writer, options load.Options, loader programLoader) int {
	if len(arguments) == 0 {
		printHelp(stdout)
		return 0
	}

	switch arguments[0] {
	case "help", "-h", "--help":
		printHelp(stdout)
		return 0
	case "version", "--version":
		fmt.Fprintf(stdout, "spice %s\n", Version)
		return 0
	case "verify":
		return verify(packagePatterns(arguments[1:]), stdout, stderr, options, loader)
	case "annotations":
		return annotations(packagePatterns(arguments[1:]), stdout, stderr, options, loader)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n", arguments[0])
		printHelp(stderr)
		return 2
	}
}

func verify(patterns []string, stdout, stderr io.Writer, options load.Options, loader programLoader) int {
	result, ok := resolvePatterns(patterns, stderr, options, loader, "verification")
	if !ok {
		return 1
	}

	diagnostics := validationDiagnostics(result.Occurrences)
	if len(diagnostics) > 0 {
		for _, diagnostic := range diagnostics {
			fmt.Fprintln(stderr, diagnostic.Error())
		}
		fmt.Fprintf(stderr, "Spice verification failed: %d annotation validation error(s).\n", len(diagnostics))
		return 1
	}
	fmt.Fprintf(stdout, "Spice verification passed: %d annotations in %d Go files.\n", len(result.Occurrences), result.Files)
	return 0
}

func annotations(patterns []string, stdout, stderr io.Writer, options load.Options, loader programLoader) int {
	result, ok := resolvePatterns(patterns, stderr, options, loader, "annotation resolution")
	if !ok {
		return 1
	}
	for _, occurrence := range result.Occurrences {
		path := filepath.ToSlash(occurrence.DisplayPosition.Filename)
		fmt.Fprintf(
			stdout,
			"%s:%d %s %s @%s\n",
			path,
			occurrence.DisplayPosition.Line,
			occurrence.Target,
			occurrence.Name,
			occurrence.Annotation.Name,
		)
	}
	fmt.Fprintf(stdout, "Found %d annotations in %d Go files.\n", len(result.Occurrences), result.Files)
	return 0
}

func resolvePatterns(patterns []string, stderr io.Writer, options load.Options, loader programLoader, operation string) (resolve.Result, bool) {
	program, err := loader(context.Background(), options, patterns...)
	if err != nil {
		fmt.Fprintf(stderr, "Spice %s failed: %v\n", operation, err)
		return resolve.Result{}, false
	}
	result := resolve.Annotations(program)
	if len(result.Diagnostics) > 0 {
		for _, diagnostic := range result.Diagnostics {
			fmt.Fprintln(stderr, diagnostic.Error())
		}
		fmt.Fprintf(stderr, "Spice %s failed: %d annotation resolution error(s).\n", operation, len(result.Diagnostics))
		return result, false
	}
	return result, true
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

func printHelp(writer io.Writer) {
	fmt.Fprintln(writer, `Spice Framework for Go

Usage:
  spice version
  spice verify [package-pattern ...]
  spice annotations [package-pattern ...]

Commands:
  version      Print the Spice version.
  verify       Load, resolve, and validate Spice annotations for Go packages.
  annotations  List annotations and their exact typed declarations.`)
}
