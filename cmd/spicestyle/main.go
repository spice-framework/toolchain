// Command spicestyle verifies the Spice java-structured source profile.
package main

import (
	"io"
	"os"
	"strings"

	"github.com/spice-framework/toolchain/internal/cli"
)

func main() {
	//nolint:forbidigo // This process entrypoint owns the command exit status.
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(arguments []string, stdout, stderr io.Writer) int {
	return cli.Run(normalizeArguments(arguments), stdout, stderr)
}

func normalizeArguments(arguments []string) []string {
	result := []string{"verify"}
	configured := false
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		switch argument {
		case "-json":
			result = append(result, "--format=json")
		case "--config", "-config":
			configured = true
			if index+1 < len(arguments) {
				index++
				result = append(result, "--style="+arguments[index])
			} else {
				result = append(result, "--style")
			}
		default:
			if value, found := strings.CutPrefix(argument, "--config="); found {
				result = append(result, "--style="+value)
				configured = true
			} else if value, found := strings.CutPrefix(argument, "-config="); found {
				result = append(result, "--style="+value)
				configured = true
			} else {
				result = append(result, argument)
			}
		}
	}
	if !configured {
		result = append(result, "--style=.spice/style.json")
	}
	return result
}
