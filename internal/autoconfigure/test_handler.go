package autoconfigure

import "github.com/StevenBuglione/spice/internal/cli"

// DefaultTestHandler constructs the focused module-test command.
func DefaultTestHandler(runtime *cli.Runtime) (cli.Handler, error) {
	return cli.NewTestHandler(runtime)
}
