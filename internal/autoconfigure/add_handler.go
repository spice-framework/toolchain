package autoconfigure

import "github.com/spice-framework/spice/internal/cli"

// DefaultAddHandler constructs the guarded Go module dependency command.
func DefaultAddHandler(runtime *cli.Runtime) (cli.Handler, error) {
	return cli.NewAddHandler(runtime)
}
