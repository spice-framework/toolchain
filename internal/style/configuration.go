package style

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

const maximumConfigurationBytes = 1 << 20

// Configuration is the strict schema-one java-structured style contract.
type Configuration struct {
	SchemaVersion             int                        `json:"schemaVersion"`
	Profile                   string                     `json:"profile"`
	SourceRoots               []string                   `json:"sourceRoots"`
	GeneratedRoots            []string                   `json:"generatedRoots"`
	Rules                     Rules                      `json:"rules"`
	PublicRoutes              []PublicRoute              `json:"publicRoutes"`
	AllowedBoundaryFiles      []string                   `json:"allowedBoundaryFiles"`
	PackageFunctionExceptions []PackageFunctionException `json:"packageFunctionExceptions"`
}

// LoadConfiguration reads and validates one bounded, strict configuration.
func LoadConfiguration(path string) (Configuration, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return Configuration{}, fmt.Errorf("open Spice style configuration: %w", err)
	}
	content, readErr := io.ReadAll(io.LimitReader(file, maximumConfigurationBytes+1))
	closeErr := file.Close()
	if readErr != nil {
		return Configuration{}, fmt.Errorf(
			"read Spice style configuration: %w",
			errors.Join(readErr, closeErr),
		)
	}
	if closeErr != nil {
		return Configuration{}, fmt.Errorf("close Spice style configuration: %w", closeErr)
	}
	if len(content) > maximumConfigurationBytes {
		return Configuration{}, newConfigurationError("file exceeds 1 MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	var configuration Configuration
	if err := decoder.Decode(&configuration); err != nil {
		return Configuration{}, fmt.Errorf("decode Spice style configuration: %w", err)
	}
	if err := requireJSONEnd(decoder); err != nil {
		return Configuration{}, err
	}
	if err := configuration.Validate(); err != nil {
		return Configuration{}, err
	}
	return configuration.clone(), nil
}

func requireJSONEnd(decoder *json.Decoder) error {
	var trailing json.RawMessage
	err := decoder.Decode(&trailing)
	if errors.Is(err, io.EOF) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("decode trailing Spice style configuration data: %w", err)
	}
	return newConfigurationError("trailing JSON value")
}

// Validate checks the closed schema and every path/suppression boundary.
func (configuration Configuration) Validate() error {
	if configuration.SchemaVersion != 1 {
		return newConfigurationError("schemaVersion must equal 1")
	}
	if configuration.Profile != "java-structured" {
		return newConfigurationError("profile must equal java-structured")
	}
	if err := validateRoots("sourceRoots", configuration.SourceRoots, false); err != nil {
		return err
	}
	if err := validateRoots("generatedRoots", configuration.GeneratedRoots, true); err != nil {
		return err
	}
	if err := configuration.Rules.validate(); err != nil {
		return err
	}
	for index, route := range configuration.PublicRoutes {
		if strings.TrimSpace(route.Package) == "" ||
			strings.TrimSpace(route.Receiver) == "" ||
			strings.TrimSpace(route.Method) == "" ||
			strings.TrimSpace(route.Reason) == "" ||
			strings.TrimSpace(route.Issue) == "" {
			return newConfigurationError(fmt.Sprintf("publicRoutes[%d] is incomplete", index))
		}
	}
	for index, pattern := range configuration.AllowedBoundaryFiles {
		if err := validateGlob(pattern); err != nil {
			return newConfigurationError(fmt.Sprintf("allowedBoundaryFiles[%d]: %s", index, err))
		}
	}
	for index, exception := range configuration.PackageFunctionExceptions {
		if err := exception.validate(); err != nil {
			return newConfigurationError(fmt.Sprintf("packageFunctionExceptions[%d]: %s", index, err))
		}
	}
	return nil
}

func validateRoots(field string, roots []string, allowEmpty bool) error {
	if len(roots) == 0 && !allowEmpty {
		return newConfigurationError(field + " must not be empty")
	}
	seen := make(map[string]struct{}, len(roots))
	for _, root := range roots {
		clean := filepath.ToSlash(filepath.Clean(root))
		if root == "" || filepath.IsAbs(root) || clean == "." || clean == ".." ||
			strings.HasPrefix(clean, "../") {
			return newConfigurationError(field + " must contain safe relative paths")
		}
		if _, duplicate := seen[clean]; duplicate {
			return newConfigurationError(field + " contains a duplicate path")
		}
		seen[clean] = struct{}{}
	}
	return nil
}

func validateGlob(pattern string) error {
	if pattern == "" || filepath.IsAbs(pattern) || strings.Contains(pattern, "\\") {
		return errors.New("glob must be a non-empty slash-separated relative pattern")
	}
	if strings.Contains(pattern, "..") {
		return errors.New("glob must not contain parent traversal")
	}
	return nil
}

func (exception PackageFunctionException) validate() error {
	if err := validateGlob(exception.Glob); err != nil {
		return err
	}
	if strings.TrimSpace(exception.Reason) == "" {
		return errors.New("reason is required")
	}
	selectors := 0
	if exception.Symbol != "" {
		selectors++
	}
	if exception.SymbolPattern != "" {
		selectors++
		if _, err := regexp.Compile(exception.SymbolPattern); err != nil {
			return errors.New("symbolPattern is invalid")
		}
	}
	if exception.ContributionKind != "" {
		selectors++
	}
	if selectors != 1 {
		return errors.New("exactly one symbol, symbolPattern, or contributionKind is required")
	}
	if exception.Maximum < 0 {
		return errors.New("maximum must not be negative")
	}
	return nil
}

func (configuration Configuration) clone() Configuration {
	configuration.SourceRoots = slices.Clone(configuration.SourceRoots)
	configuration.GeneratedRoots = slices.Clone(configuration.GeneratedRoots)
	configuration.PublicRoutes = slices.Clone(configuration.PublicRoutes)
	configuration.AllowedBoundaryFiles = slices.Clone(configuration.AllowedBoundaryFiles)
	configuration.PackageFunctionExceptions = slices.Clone(configuration.PackageFunctionExceptions)
	return configuration
}
