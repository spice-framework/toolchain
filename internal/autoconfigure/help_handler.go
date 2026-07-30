package autoconfigure

import "github.com/StevenBuglione/spice/internal/cli"

// DefaultHelpHandler constructs the help command.
func DefaultHelpHandler(runtime *cli.Runtime) (cli.Handler, error) {
	return cli.NewHelpHandler(runtime)
}
