package autoconfigure

import "github.com/spice-framework/spice/internal/cli"

// DefaultRunHandler constructs the application execution command.
func DefaultRunHandler(runtime *cli.Runtime) (cli.Handler, error) {
	return cli.NewRunHandler(runtime)
}
