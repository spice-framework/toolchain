// Command libraryreleaseacceptance runs the hosted cross-producer release proof.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spice-framework/toolchain/internal/libraryreleaseacceptance"
)

func main() {
	//nolint:forbidigo // This process entrypoint must expose acceptance failure to its caller.
	os.Exit(run(libraryreleaseacceptance.Run, os.Stdout, os.Stderr))
}

func run(
	execute func(context.Context, string, io.Writer) error,
	stdout io.Writer,
	stderr io.Writer,
) int {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	if err := execute(ctx, ".", stdout); err != nil {
		if _, writeErr := fmt.Fprintf(
			stderr,
			"library release cross-producer acceptance failed: %v\n",
			err,
		); writeErr != nil {
			return 1
		}
		return 1
	}
	return 0
}
