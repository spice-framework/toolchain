// @import { Module } from "github.com/StevenBuglione/spice/annotation/modulith"

// Package cli implements the Spice command-line interface.
//
// @Module
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	codegen "github.com/StevenBuglione/spice/compiler/generate"
	"github.com/StevenBuglione/spice/compiler/load"
	"github.com/StevenBuglione/spice/compiler/resolve"
)

// Version is the version reported by the Spice CLI. Release builds replace the
// development value through Go's link-time string-variable mechanism.
var Version = "0.1.0-dev"

const legacyStarterSelectionPath = ".spice/starters.json"

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
		return verifyCommand(arguments[1:], stdout, stderr, options, loader)
	case "annotations":
		return annotationsCommand(
			arguments[1:],
			stdout,
			stderr,
			options,
			loader,
		)
	case "modules", "beans":
		return architectureCommand(
			arguments,
			stdout,
			stderr,
			options,
			loader,
		)
	case "generated":
		return generatedCommand(arguments[1:], stdout, stderr, options)
	case "test":
		return moduleTestCommand(arguments[1:], stdout, stderr, options, loader, tester)
	case "generate":
		return generateCommand(arguments[1:], stdout, stderr, options, loader)
	case "build":
		return buildCommand(arguments[1:], stdout, stderr, options, loader, builder)
	case "run":
		return runCommand(arguments[1:], stdout, stderr, options, loader)
	case "dev":
		return devCommand(arguments[1:], stdout, stderr, options, loader)
	case "lsp":
		return lspCommand(arguments[1:], os.Stdin, stdout, stderr, options, loader)
	default:
		return unknownCommand(arguments[0], stderr)
	}
}

func architectureCommand(
	arguments []string,
	stdout io.Writer,
	stderr io.Writer,
	options load.Options,
	loader programLoader,
) int {
	if arguments[0] == "modules" {
		return modulesCommand(arguments[1:], stdout, stderr, options, loader)
	}
	return beansCommand(arguments[1:], stdout, stderr, options, loader)
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
	return resolvePatternsContext(
		context.Background(),
		patterns,
		stderr,
		options,
		loader,
		operation,
	)
}

func resolvePatternsContext(
	ctx context.Context,
	patterns []string,
	stderr io.Writer,
	options load.Options,
	loader programLoader,
	operation string,
) (*load.Program, resolve.Result, bool) {
	program, err := loader(ctx, options, patterns...)
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
	result.PrepareGeneratedApplicationEntrypoints = true
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

func rejectLegacyStarterSelection(options load.Options) error {
	directory := options.Dir
	if directory == "" {
		directory = "."
	}
	path := filepath.Join(directory, filepath.FromSlash(legacyStarterSelectionPath))
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf(
			"inspect retired starter selection %s: %w",
			legacyStarterSelectionPath,
			err,
		)
	}
	return fmt.Errorf(
		"retired starter selection %s exists (%s); remove it and blank-import a dedicated .../autoconfigure package instead",
		legacyStarterSelectionPath,
		info.Mode(),
	)
}

func packagePatterns(arguments []string) []string {
	if len(arguments) == 0 {
		return []string{"./..."}
	}
	return append([]string(nil), arguments...)
}

func printHelp(writer io.Writer) error {
	_, err := fmt.Fprintln(writer, `Spice Framework for Go

Usage:
  spice version
  spice verify [--format text|json] [package-pattern ...]
  spice annotations [package-pattern ...]
  spice annotations list [package-pattern ...]
  spice annotations doctor [package-pattern ...]
  spice modules [--format json|mermaid|plantuml] [--focus module] [package-pattern ...]
  spice beans --explain [--format text|json] [package-pattern ...]
  spice generated (--source path | --generated path) [--line n] [--target name] [--format text|json]
  spice test --module module [--race] [--count n] [--run regexp] [--timeout duration] [package-pattern ...]
  spice generate [--target name] [--check] [--diff] [package-pattern ...]
  spice build [--target name] [package-pattern ...]
  spice run [--target name] [package-pattern ...] [-- application-argument ...]
  spice dev [--target name] [dev-option ...] [package-pattern ...] [-- application-argument ...]
  spice lsp

Commands:
  version      Print the Spice version.
  verify       Load, resolve, and validate Spice annotations for Go packages.
  annotations  List occurrences, inspect descriptors, or verify annotation tools.
  modules      Validate and render application-module documentation.
  beans        Explain provider selection and imported library defaults.
  generated    Locate generated code from source, or source from generated code.
  test         Validate and run one focused application-module test graph.
  generate     Render and safely apply or check generated application code.
  build        Generate an application and run the standard trimpath build.
  run          Generate, build, and execute a package-main application.
  dev          Watch, regenerate, build, and gracefully restart an application.
  lsp          Serve editor-neutral Spice language features over stdio.

Development options:
  --quiet duration         Debounce quiet period (default 150ms).
  --max-delay duration     Maximum change-burst delay (default 2s).
  --poll duration          Portable recursive polling interval (default 500ms).
  --stop-timeout duration  Graceful process-stop bound (default 15s).
  --include pattern        Add a watched workspace-relative path pattern.
  --exclude pattern        Exclude a workspace-relative path pattern.

Library auto-configuration:
  Blank-import a dedicated .../autoconfigure package to select statically
  decoded, direct-call default beans. Use spice beans --explain to inspect
  selection, replacement, dependency backoff, and module provenance.`)
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
