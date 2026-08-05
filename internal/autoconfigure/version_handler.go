package autoconfigure

import "github.com/spice-framework/spice/internal/cli"

// DefaultVersionHandler constructs the version command.
func DefaultVersionHandler(runtime *cli.Runtime) (cli.Handler, error) {
	return cli.NewVersionHandler(runtime)
}
