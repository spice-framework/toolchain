package autoconfigure

import "github.com/StevenBuglione/spice/internal/cli"

// DefaultDevHandler constructs the development-loop command.
func DefaultDevHandler(runtime *cli.Runtime) (cli.Handler, error) {
	return cli.NewDevHandler(runtime)
}
