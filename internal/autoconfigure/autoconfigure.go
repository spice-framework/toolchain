// Package autoconfigure provides the explicitly imported default Spice command
// bean used by the production CLI application.
package autoconfigure

import (
	"github.com/StevenBuglione/spice/internal/cli"
	"github.com/StevenBuglione/spice/starter"
)

// DefaultCommand constructs the default production Spice command.
func DefaultCommand() *cli.Command {
	return cli.NewCommand()
}

// SpiceAutoConfiguration declares the default command bean. The descriptor is
// statically decoded during analysis and is never executed by the compiler.
func SpiceAutoConfiguration() starter.AutoConfiguration {
	return starter.AutoConfiguration{
		Review: "docs/dogfooding-readiness.md",
		Beans: []starter.AutoBean{{
			Factory:  DefaultCommand,
			Name:     "command",
			Fallback: true,
		}},
	}
}
