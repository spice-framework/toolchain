package autoconfigure

import "github.com/StevenBuglione/spice/internal/cli"

// DefaultAnnotationsHandler constructs the annotation command.
func DefaultAnnotationsHandler(runtime *cli.Runtime) (cli.Handler, error) {
	return cli.NewAnnotationsHandler(runtime)
}
