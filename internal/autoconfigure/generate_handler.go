package autoconfigure

import "github.com/StevenBuglione/spice/internal/cli"

// DefaultGenerateHandler constructs the generation command.
func DefaultGenerateHandler(runtime *cli.Runtime) (cli.Handler, error) {
	return cli.NewGenerateHandler(runtime)
}
