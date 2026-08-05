package autoconfigure

import "github.com/spice-framework/spice/internal/cli"

// DefaultCommand assembles every generated interface handler.
func DefaultCommand(handlers []cli.Handler) (*cli.Command, error) {
	return cli.NewCommand(handlers)
}
