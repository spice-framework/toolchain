package annotationhost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/StevenBuglione/spice/annotation"
)

const maximumGoCommandStderr = 256 << 10

// ModuleIdentity is one selected Go module and its visible replacement.
type ModuleIdentity struct {
	Path        string
	Version     string
	Directory   string
	Replacement *ModuleIdentity
}

// PackageIdentity records standard Go provenance for one package.
type PackageIdentity struct {
	Path   string
	Module ModuleIdentity
}

type goListPackage struct {
	ImportPath string        `json:"ImportPath"`
	Error      *goListError  `json:"Error"`
	Module     *goListModule `json:"Module"`
}

type goListError struct {
	Err string `json:"Err"`
}

type goListModule struct {
	Path    string        `json:"Path"`
	Version string        `json:"Version"`
	Dir     string        `json:"Dir"`
	Replace *goListModule `json:"Replace"`
}

// ResolvePackage asks the standard Go command for selected module provenance.
// It is always offline and never edits go.mod or go.sum.
func ResolvePackage(
	ctx context.Context,
	module TargetModule,
	packagePath string,
	environment []string,
) (PackageIdentity, error) {
	if ctx == nil {
		return PackageIdentity{}, errors.New(
			"resolve annotation package context must not be nil",
		)
	}
	mode := moduleMode(module.Root)
	command := exec.CommandContext( // #nosec G204 -- executable and flags are fixed; packagePath is one argument.
		ctx,
		"go",
		"list",
		"-json",
		"-mod="+mode,
		packagePath,
	)
	command.Dir = module.Root
	command.Env = offlineEnvironment(environment, mode)
	var stdout bytes.Buffer
	stderr := newBoundedBuffer(maximumGoCommandStderr)
	command.Stdout = &stdout
	command.Stderr = stderr
	if err := command.Run(); err != nil {
		return PackageIdentity{}, fmt.Errorf(
			"resolve annotation package %q with offline go list: %w%s",
			packagePath,
			err,
			renderStderr(stderr.String()),
		)
	}
	var listed goListPackage
	decoder := json.NewDecoder(&stdout)
	if err := decoder.Decode(&listed); err != nil {
		return PackageIdentity{}, fmt.Errorf(
			"decode go list provenance for %q: %w",
			packagePath,
			err,
		)
	}
	if listed.Error != nil {
		return PackageIdentity{}, fmt.Errorf(
			"go list could not resolve annotation package %q: %s",
			packagePath,
			listed.Error.Err,
		)
	}
	if listed.ImportPath != packagePath || listed.Module == nil {
		return PackageIdentity{}, fmt.Errorf(
			"go list returned unexpected annotation package identity %q for %q",
			listed.ImportPath,
			packagePath,
		)
	}
	return PackageIdentity{
		Path:   listed.ImportPath,
		Module: moduleIdentity(listed.Module),
	}, nil
}

func moduleIdentity(module *goListModule) ModuleIdentity {
	if module == nil {
		return ModuleIdentity{}
	}
	result := ModuleIdentity{
		Path:      module.Path,
		Version:   module.Version,
		Directory: filepath.Clean(module.Dir),
	}
	if module.Replace != nil {
		replacement := moduleIdentity(module.Replace)
		result.Replacement = &replacement
	}
	return result
}

// ValidateDescriptorToolModule requires descriptor and executable packages to
// resolve from the same module version and replacement identity.
func ValidateDescriptorToolModule(
	descriptor annotation.ModuleProvenance,
	tool PackageIdentity,
) error {
	resolved := tool.Module
	if descriptor.Path != resolved.Path ||
		descriptor.Version != resolved.Version {
		return fmt.Errorf(
			"annotation descriptor module %s@%s does not match tool module %s@%s",
			descriptor.Path,
			descriptor.Version,
			resolved.Path,
			resolved.Version,
		)
	}
	if descriptor.Version == "" &&
		descriptor.ReplacementPath == "" &&
		resolved.Replacement == nil &&
		!sameDirectory(descriptor.Directory, resolved.Directory) {
		return fmt.Errorf(
			"annotation descriptor and tool use different local source for module %s",
			descriptor.Path,
		)
	}
	replacementPath := ""
	replacementVersion := ""
	replacementDirectory := ""
	if resolved.Replacement != nil {
		replacementPath = resolved.Replacement.Path
		replacementVersion = resolved.Replacement.Version
		replacementDirectory = resolved.Replacement.Directory
	}
	if descriptor.ReplacementPath != replacementPath ||
		descriptor.ReplacementVersion != replacementVersion ||
		!sameDirectory(descriptor.ReplacementDir, replacementDirectory) {
		return fmt.Errorf(
			"annotation descriptor and tool use different replacements for module %s@%s",
			descriptor.Path,
			descriptor.Version,
		)
	}
	return nil
}

func sameDirectory(left, right string) bool {
	if left == "" || right == "" {
		return left == right
	}
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func moduleMode(root string) string {
	if _, err := os.Stat(filepath.Join(root, "vendor", "modules.txt")); err == nil {
		return "vendor"
	}
	return "readonly"
}

func offlineEnvironment(environment []string, mode string) []string {
	if environment == nil {
		environment = os.Environ()
	}
	result := replaceEnvironmentValue(environment, "GOPROXY", "off")
	flags := environmentValue(result, "GOFLAGS")
	fields := strings.Fields(flags)
	filtered := make([]string, 0, len(fields)+1)
	for index := 0; index < len(fields); index++ {
		if fields[index] == "-mod" {
			index++
			continue
		}
		if strings.HasPrefix(fields[index], "-mod=") {
			continue
		}
		filtered = append(filtered, fields[index])
	}
	filtered = append(filtered, "-mod="+mode)
	return replaceEnvironmentValue(
		result,
		"GOFLAGS",
		strings.Join(filtered, " "),
	)
}

func replaceEnvironmentValue(
	environment []string,
	name string,
	value string,
) []string {
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		key, _, found := strings.Cut(entry, "=")
		if found && strings.EqualFold(key, name) {
			continue
		}
		result = append(result, entry)
	}
	return append(result, name+"="+value)
}

func environmentValue(environment []string, name string) string {
	for _, entry := range environment {
		key, value, found := strings.Cut(entry, "=")
		if found && strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}

func renderStderr(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return ": " + value
}
