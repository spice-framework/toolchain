package autoconfigure

import "github.com/StevenBuglione/spice/internal/cli"

// DefaultModulesHandler constructs the module command.
func DefaultModulesHandler(runtime *cli.Runtime) (cli.Handler, error) {
	return cli.NewModulesHandler(runtime)
}
