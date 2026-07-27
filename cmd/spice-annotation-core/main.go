package main

import (
	"context"
	"fmt"
	"os"

	"github.com/StevenBuglione/spice/annotation/sdk/protocol"
	"github.com/StevenBuglione/spice/internal/annotationcore"
)

func main() {
	// Entrypoints must translate the protocol result to a process exit status.
	//nolint:forbidigo // This package-main boundary alone owns the process status.
	os.Exit(run(os.Args, os.Stdin, os.Stdout, os.Stderr))
}

func run(arguments []string, stdin *os.File, stdout, stderr *os.File) int {
	if len(arguments) != 2 || arguments[1] != "--spice-stdio" {
		if _, err := fmt.Fprintln(
			stderr,
			"spice-annotation-core requires --spice-stdio",
		); err != nil {
			return 1
		}
		return 2
	}
	if err := protocol.Serve(
		context.Background(),
		stdin,
		stdout,
		annotationcore.New(),
	); err != nil {
		if _, writeErr := fmt.Fprintf(
			stderr,
			"spice-annotation-core: %v\n",
			err,
		); writeErr != nil {
			return 1
		}
		return 1
	}
	return 0
}
