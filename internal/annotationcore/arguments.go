package annotationcore

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/StevenBuglione/spice/annotation"
	"github.com/StevenBuglione/spice/annotation/sdk/protocol"
)

func requireDescriptor(
	invocation protocol.Invocation,
	packagePath string,
	symbol string,
) error {
	if invocation.DescriptorPackage == packagePath &&
		invocation.DescriptorSymbol == symbol {
		return nil
	}
	return fmt.Errorf(
		"annotation handler for %s.%s received descriptor %s.%s",
		packagePath,
		symbol,
		invocation.DescriptorPackage,
		invocation.DescriptorSymbol,
	)
}

func bindArguments(
	invocation protocol.Invocation,
	positional string,
	allowed ...string,
) (map[string]protocol.Argument, error) {
	result := make(map[string]protocol.Argument, len(invocation.Arguments))
	for _, argument := range invocation.Arguments {
		name := argument.Name
		if argument.Positional {
			if name != "" || positional == "" {
				return nil, fmt.Errorf(
					"annotation %s contains an unsupported positional argument",
					invocation.CanonicalName,
				)
			}
			name = positional
		} else if name == "" {
			return nil, fmt.Errorf(
				"annotation %s contains an unnamed non-positional argument",
				invocation.CanonicalName,
			)
		}
		if !slices.Contains(allowed, name) {
			return nil, fmt.Errorf(
				"annotation %s contains unsupported argument %q",
				invocation.CanonicalName,
				name,
			)
		}
		if _, duplicate := result[name]; duplicate {
			return nil, fmt.Errorf(
				"annotation %s repeats argument %q",
				invocation.CanonicalName,
				name,
			)
		}
		result[name] = argument
	}
	return result, nil
}

func stringArgument(
	arguments map[string]protocol.Argument,
	name string,
	required bool,
) (string, error) {
	argument, found := arguments[name]
	if !found {
		if required {
			return "", fmt.Errorf(
				"annotation argument %q is required",
				name,
			)
		}
		return "", nil
	}
	if argument.Kind != annotation.KindString {
		return "", fmt.Errorf(
			"annotation argument %q must be a string",
			name,
		)
	}
	var result string
	if err := json.Unmarshal(argument.Value, &result); err != nil {
		return "", fmt.Errorf(
			"decode annotation string argument %q: %w",
			name,
			err,
		)
	}
	if required && strings.TrimSpace(result) == "" {
		return "", fmt.Errorf(
			"annotation argument %q must not be empty",
			name,
		)
	}
	return result, nil
}

func stringListArgument(
	arguments map[string]protocol.Argument,
	name string,
) ([]string, error) {
	argument, found := arguments[name]
	if !found {
		return nil, nil
	}
	if argument.Kind != annotation.KindList {
		return nil, fmt.Errorf(
			"annotation argument %q must be a list",
			name,
		)
	}
	var result []string
	if err := json.Unmarshal(argument.Value, &result); err != nil {
		return nil, fmt.Errorf(
			"decode annotation string-list argument %q: %w",
			name,
			err,
		)
	}
	return result, nil
}

func booleanArgument(
	arguments map[string]protocol.Argument,
	name string,
) (bool, error) {
	argument, found := arguments[name]
	if !found {
		return false, nil
	}
	if argument.Kind != annotation.KindBoolean {
		return false, fmt.Errorf(
			"annotation argument %q must be boolean",
			name,
		)
	}
	var result bool
	if err := json.Unmarshal(argument.Value, &result); err != nil {
		return false, fmt.Errorf(
			"decode annotation boolean argument %q: %w",
			name,
			err,
		)
	}
	return result, nil
}

func integerArgument(
	arguments map[string]protocol.Argument,
	name string,
) (int64, error) {
	argument, found := arguments[name]
	if !found {
		return 0, nil
	}
	if argument.Kind != annotation.KindInteger {
		return 0, fmt.Errorf(
			"annotation argument %q must be an integer",
			name,
		)
	}
	var result int64
	if err := json.Unmarshal(argument.Value, &result); err != nil {
		return 0, fmt.Errorf(
			"decode annotation integer argument %q: %w",
			name,
			err,
		)
	}
	return result, nil
}
