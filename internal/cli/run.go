// Package cli implements the Spice command-line interface.
package cli

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/StevenBuglione/spice/compiler/scan"
)

const Version = "0.1.0-dev"

// Run executes Spice and returns a process exit code.
func Run(arguments []string, stdout, stderr io.Writer) int {
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
		return verify(pathArgument(arguments[1:]), stdout, stderr)
	case "annotations":
		return annotations(pathArgument(arguments[1:]), stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n", arguments[0])
		printHelp(stderr)
		return 2
	}
}

func verify(root string, stdout, stderr io.Writer) int {
	result, err := scan.Tree(root)
	if err != nil {
		fmt.Fprintf(stderr, "Spice verification failed: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Spice verification passed: %d annotations in %d Go files.\n", len(result.Occurrences), result.Files)
	return 0
}

func annotations(root string, stdout, stderr io.Writer) int {
	result, err := scan.Tree(root)
	if err != nil {
		fmt.Fprintf(stderr, "Spice annotation scan failed: %v\n", err)
		return 1
	}
	for _, occurrence := range result.Occurrences {
		path := filepath.ToSlash(occurrence.File)
		fmt.Fprintf(
			stdout,
			"%s:%d %s %s @%s\n",
			path,
			occurrence.Annotation.Position.Line,
			occurrence.Target,
			occurrence.Name,
			occurrence.Annotation.Name,
		)
	}
	fmt.Fprintf(stdout, "Found %d annotations in %d Go files.\n", len(result.Occurrences), result.Files)
	return 0
}

func pathArgument(arguments []string) string {
	if len(arguments) == 0 {
		return "."
	}
	return scan.PathRoot(arguments[0])
}

func printHelp(writer io.Writer) {
	fmt.Fprintln(writer, `Spice Framework for Go

Usage:
  spice version
  spice verify [path|./...]
  spice annotations [path|./...]

Commands:
  version      Print the Spice version.
  verify       Parse and validate Spice annotations in Go source.
  annotations  List annotations and their associated declarations.`)
}
