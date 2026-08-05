package autoconfigure

import "github.com/spice-framework/spice/internal/cli"

// DefaultBuildHandler constructs the build command.
func DefaultBuildHandler(runtime *cli.Runtime) (cli.Handler, error) {
	return cli.NewBuildHandler(runtime)
}
