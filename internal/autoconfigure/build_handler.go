package autoconfigure

import "github.com/StevenBuglione/spice/internal/cli"

// DefaultBuildHandler constructs the build command.
func DefaultBuildHandler(runtime *cli.Runtime) (cli.Handler, error) {
	return cli.NewBuildHandler(runtime)
}
