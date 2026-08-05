package autoconfigure

import "github.com/spice-framework/spice/internal/cli"

// DefaultGeneratedHandler constructs the generated-source command.
func DefaultGeneratedHandler(runtime *cli.Runtime) (cli.Handler, error) {
	return cli.NewGeneratedHandler(runtime)
}
