package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"example.com/spice-annotation-fixture/internal/handler"
	"github.com/spice-framework/spice/annotation/sdk/protocol"
)

func main() {
	os.Exit(run(os.Args, os.Stdin, os.Stdout, os.Stderr))
}

func run(
	arguments []string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
) int {
	if len(arguments) != 2 || arguments[1] != "--spice-stdio" {
		if _, err := fmt.Fprintln(
			stderr,
			"spice-annotations requires --spice-stdio",
		); err != nil {
			return 1
		}
		return 2
	}
	if err := protocol.Serve(
		context.Background(),
		stdin,
		stdout,
		handler.New(),
	); err != nil {
		if _, writeErr := fmt.Fprintf(
			stderr,
			"spice-annotations: %v\n",
			err,
		); writeErr != nil {
			return 1
		}
		return 1
	}
	return 0
}
