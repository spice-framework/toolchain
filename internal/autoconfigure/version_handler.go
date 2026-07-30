package autoconfigure

import "github.com/StevenBuglione/spice/internal/cli"

// DefaultVersionHandler constructs the version command.
func DefaultVersionHandler(runtime *cli.Runtime) (cli.Handler, error) {
	return cli.NewVersionHandler(runtime)
}
