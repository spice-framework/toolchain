// Command spice-bootstrap is the ordinary-Go stage-zero Spice compiler.
//
// It must never import a generated application package so it remains capable
// of regenerating the production Spice command from handwritten source.
package main

import (
	"os"

	"github.com/spice-framework/spice/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
