package cli

import "io"

// Command is the generated production Spice CLI bean. It deliberately keeps
// command parsing and compiler execution in the existing package functions;
// generated construction owns only application assembly and lifecycle.
type Command struct{}

// NewCommand constructs an isolated Spice command bean.
func NewCommand() *Command {
	return &Command{}
}

// Run executes one Spice command and returns its process exit code.
func (command *Command) Run(
	arguments []string,
	stdout io.Writer,
	stderr io.Writer,
) int {
	if command == nil {
		if err := writef(stderr, "Spice command is unavailable.\n"); err != nil {
			return 1
		}
		return 1
	}
	return Run(arguments, stdout, stderr)
}
