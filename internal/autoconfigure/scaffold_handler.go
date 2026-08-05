package autoconfigure

import "github.com/StevenBuglione/spice/internal/cli"

// DefaultScaffoldHandler constructs the clean-room application scaffold command.
func DefaultScaffoldHandler(runtime *cli.Runtime) (cli.Handler, error) {
	return cli.NewScaffoldHandler(runtime)
}
