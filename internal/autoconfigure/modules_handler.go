package autoconfigure

import "github.com/spice-framework/spice/internal/cli"

// DefaultModulesHandler constructs the module command.
func DefaultModulesHandler(runtime *cli.Runtime) (cli.Handler, error) {
	return cli.NewModulesHandler(runtime)
}
