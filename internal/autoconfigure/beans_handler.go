package autoconfigure

import "github.com/StevenBuglione/spice/internal/cli"

// DefaultBeansHandler constructs the bean explanation command.
func DefaultBeansHandler(runtime *cli.Runtime) (cli.Handler, error) {
	return cli.NewBeansHandler(runtime)
}
