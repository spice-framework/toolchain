// Command parity measures equivalent warm Petclinic source-edit feedback.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

type manifest struct {
	Schema    string     `json:"schema"`
	Reference reference  `json:"reference"`
	Samples   int        `json:"samples"`
	Warmups   int        `json:"warmups"`
	Scenarios []scenario `json:"scenarios"`
}

type reference struct {
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
	SpringBoot string `json:"spring_boot"`
	Java       string `json:"java"`
}

type scenario struct {
	Name            string  `json:"name"`
	SpiceSource     string  `json:"spice_source"`
	SpringSource    string  `json:"spring_source"`
	MaximumSpiceP90 float64 `json:"maximum_spice_p90_ms"`
	MaximumP90Ratio float64 `json:"maximum_ratio_to_spring_p90"`
	Rationale       string  `json:"rationale"`
}

type measurement struct {
	Schema      string    `json:"schema"`
	Scenario    string    `json:"scenario"`
	SpiceMS     []float64 `json:"spice_ms"`
	SpringMS    []float64 `json:"spring_ms"`
	SpiceP90MS  float64   `json:"spice_p90_ms"`
	SpringP90MS float64   `json:"spring_p90_ms"`
	P90Ratio    float64   `json:"p90_ratio"`
}

func main() {
	os.Exit(execute()) // Entrypoint exception: return parity failures to make.
}

func execute() int {
	reporter := log.New(os.Stderr, "", 0)
	springRoot := flag.String("spring", "", "pinned Spring Petclinic checkout")
	flag.Parse()
	if *springRoot == "" {
		reporter.Print("parity failed: -spring is required")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	result, err := run(ctx, *springRoot)
	if err != nil {
		reporter.Printf("parity failed: %v", err)
		return 1
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		reporter.Printf("parity failed: encode result: %v", err)
		return 1
	}
	return 0
}

func run(ctx context.Context, springRoot string) (measurement, error) {
	repositoryRoot, err := findRepositoryRoot()
	if err != nil {
		return measurement{}, err
	}
	spec, err := readManifest(repositoryRoot)
	if err != nil {
		return measurement{}, err
	}
	if len(spec.Scenarios) != 1 {
		return measurement{}, fmt.Errorf("parity manifest has %d scenarios, require 1", len(spec.Scenarios))
	}
	return runComparison(
		ctx,
		repositoryRoot,
		springRoot,
		spec,
		commandRun,
		capture,
	)
}

type commandRunner func(context.Context, string, string, ...string) error

type outputCapture func(context.Context, string, string, ...string) (string, error)

func runComparison(
	ctx context.Context,
	repositoryRoot string,
	springRoot string,
	spec manifest,
	execute commandRunner,
	captureOutput outputCapture,
) (measurement, error) {
	head, err := captureOutput(ctx, springRoot, "git", "rev-parse", "HEAD")
	if err != nil {
		return measurement{}, err
	}
	if strings.TrimSpace(head) != spec.Reference.Commit {
		return measurement{}, fmt.Errorf(
			"spring Petclinic checkout is %s, require %s",
			strings.TrimSpace(head),
			spec.Reference.Commit,
		)
	}
	item := spec.Scenarios[0]
	spiceRoot := filepath.Join(repositoryRoot, "examples", "petclinic")
	spiceCommand := []string{"go", "build", "./..."}
	springExecutable := "./mvnw"
	if runtime.GOOS == "windows" {
		springExecutable = `.\mvnw.cmd`
	}
	springCommand := []string{springExecutable, "-o", "-DskipTests", "compile"}
	spiceSamples, err := measureEdits(
		ctx,
		spiceRoot,
		item.SpiceSource,
		spiceCommand,
		spec.Warmups,
		spec.Samples,
		execute,
	)
	if err != nil {
		return measurement{}, fmt.Errorf("measure Spice feedback: %w", err)
	}
	springSamples, err := measureEdits(
		ctx,
		springRoot,
		item.SpringSource,
		springCommand,
		spec.Warmups,
		spec.Samples,
		execute,
	)
	if err != nil {
		return measurement{}, fmt.Errorf(
			"measure Spring feedback (resolve Maven dependencies explicitly before running offline): %w",
			err,
		)
	}
	result := measurement{
		Schema:      spec.Schema,
		Scenario:    item.Name,
		SpiceMS:     spiceSamples,
		SpringMS:    springSamples,
		SpiceP90MS:  percentile90(spiceSamples),
		SpringP90MS: percentile90(springSamples),
	}
	result.P90Ratio = result.SpiceP90MS / result.SpringP90MS
	if result.SpiceP90MS > item.MaximumSpiceP90 {
		return result, fmt.Errorf(
			"spice p90 %.1fms exceeds %.1fms",
			result.SpiceP90MS,
			item.MaximumSpiceP90,
		)
	}
	if result.P90Ratio > item.MaximumP90Ratio {
		return result, fmt.Errorf(
			"Spice/Spring p90 ratio %.2f exceeds %.2f",
			result.P90Ratio,
			item.MaximumP90Ratio,
		)
	}
	return result, nil
}

func measureEdits(
	ctx context.Context,
	rootPath string,
	source string,
	command []string,
	warmups int,
	samples int,
	execute commandRunner,
) (result []float64, resultErr error) {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, fmt.Errorf("open benchmark root: %w", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, root.Close())
	}()
	original, err := root.ReadFile(filepath.FromSlash(source))
	if err != nil {
		return nil, fmt.Errorf("read benchmark source %s: %w", source, err)
	}
	defer func() {
		resultErr = errors.Join(
			resultErr,
			root.WriteFile(filepath.FromSlash(source), original, 0o600),
		)
	}()
	for index := range warmups + samples {
		probe := fmt.Appendf(
			append([]byte(nil), original...),
			"\n// Spice parity probe %d\n",
			index,
		)
		if err := root.WriteFile(filepath.FromSlash(source), probe, 0o600); err != nil {
			return nil, fmt.Errorf("write benchmark probe: %w", err)
		}
		started := time.Now()
		if err := execute(ctx, rootPath, command[0], command[1:]...); err != nil {
			return nil, err
		}
		if index >= warmups {
			result = append(result, float64(time.Since(started))/float64(time.Millisecond))
		}
	}
	return result, nil
}

func percentile90(values []float64) float64 {
	sorted := slices.Clone(values)
	slices.Sort(sorted)
	index := int(math.Ceil(float64(len(sorted))*0.9)) - 1
	return sorted[max(index, 0)]
}

func readManifest(rootPath string) (result manifest, resultErr error) {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return manifest{}, fmt.Errorf("open repository root: %w", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, root.Close())
	}()
	content, err := root.ReadFile("benchmarks/spring-petclinic.json")
	if err != nil {
		return manifest{}, fmt.Errorf("read parity manifest: %w", err)
	}
	if err := json.Unmarshal(content, &result); err != nil {
		return manifest{}, fmt.Errorf("decode parity manifest: %w", err)
	}
	if result.Schema != "spice.spring-parity/v1" ||
		result.Samples < 1 ||
		result.Warmups < 0 {
		return manifest{}, errors.New("invalid Spring parity manifest")
	}
	return result, nil
}

func findRepositoryRoot() (string, error) {
	current, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	for {
		found, inspectErr := isSpiceRepositoryRoot(current)
		if inspectErr != nil {
			return "", inspectErr
		}
		if found {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", errors.New("spice repository root not found")
		}
		current = parent
	}
}

func isSpiceRepositoryRoot(path string) (result bool, resultErr error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return false, fmt.Errorf("open candidate repository root: %w", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, root.Close())
	}()
	content, err := root.ReadFile("go.mod")
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read candidate go.mod: %w", err)
	}
	return bytes.Contains(
		content,
		[]byte("module github.com/StevenBuglione/spice"),
	), nil
}

func commandRun(ctx context.Context, root, executable string, arguments ...string) error {
	// #nosec G204 -- executable and arguments come from the fixed parity scenario.
	process := exec.CommandContext(ctx, executable, arguments...)
	process.Dir = root
	process.Stdout = os.Stdout
	process.Stderr = os.Stderr
	if err := process.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", executable, strings.Join(arguments, " "), err)
	}
	return nil
}

func capture(ctx context.Context, root, executable string, arguments ...string) (string, error) {
	// #nosec G204 -- executable and arguments are fixed by the harness.
	process := exec.CommandContext(ctx, executable, arguments...)
	process.Dir = root
	output, err := process.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %w\n%s", executable, err, output)
	}
	return string(output), nil
}
