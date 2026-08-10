// Command spicestyle verifies the Spice java-structured source profile.
package main

import (
	"os"
	"strings"

	"github.com/spice-framework/toolchain/internal/style"
	"golang.org/x/tools/go/analysis/multichecker"
)

func main() {
	os.Args = normalizeArguments(os.Args)
	multichecker.Main(style.Analyzer)
}

func normalizeArguments(arguments []string) []string {
	result := make([]string, 0, len(arguments))
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		switch argument {
		case "--format=text", "-format=text":
			continue
		case "--format=json", "-format=json":
			result = append(result, "-json")
		case "--config", "-config":
			if index+1 < len(arguments) {
				index++
				result = append(result, "-spicestyle.config="+arguments[index])
			} else {
				result = append(result, "-spicestyle.config")
			}
		default:
			if value, found := strings.CutPrefix(argument, "--config="); found {
				result = append(result, "-spicestyle.config="+value)
			} else if value, found := strings.CutPrefix(argument, "-config="); found {
				result = append(result, "-spicestyle.config="+value)
			} else {
				result = append(result, argument)
			}
		}
	}
	return result
}
