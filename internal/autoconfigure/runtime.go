package autoconfigure

import "github.com/spice-framework/spice/internal/cli"

// DefaultRuntime constructs the production compiler and Go command seams.
func DefaultRuntime() *cli.Runtime {
	return cli.NewRuntime()
}
