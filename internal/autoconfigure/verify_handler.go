package autoconfigure

import "github.com/spice-framework/spice/internal/cli"

// DefaultVerifyHandler constructs the verification command.
func DefaultVerifyHandler(runtime *cli.Runtime) (cli.Handler, error) {
	return cli.NewVerifyHandler(runtime)
}
