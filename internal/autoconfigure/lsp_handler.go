package autoconfigure

import "github.com/spice-framework/spice/internal/cli"

// DefaultLSPHandler constructs the language-server command.
func DefaultLSPHandler(runtime *cli.Runtime) (cli.Handler, error) {
	return cli.NewLSPHandler(runtime)
}
