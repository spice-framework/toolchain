package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestPercentile90(t *testing.T) {
	t.Parallel()

	values := []float64{7, 1, 5, 2, 6, 3, 4, 8, 9, 10}
	if got := percentile90(values); got != 9 {
		t.Fatalf("percentile90() = %v, want 9", got)
	}
	if !slices.Equal(values, []float64{7, 1, 5, 2, 6, 3, 4, 8, 9, 10}) {
		t.Fatal("percentile90() mutated its input")
	}
}

func TestReadManifest(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeParityFile(
		t,
		root,
		"benchmarks/spring-petclinic.json",
		`{"schema":"spice.spring-parity/v1","samples":3,"warmups":1,"scenarios":[]}`,
	)
	result, err := readManifest(root)
	if err != nil {
		t.Fatalf("readManifest() error = %v", err)
	}
	if result.Samples != 3 || result.Warmups != 1 {
		t.Fatalf("readManifest() = %+v", result)
	}

	writeParityFile(
		t,
		root,
		"benchmarks/spring-petclinic.json",
		`{"schema":"wrong","samples":0}`,
	)
	if _, err := readManifest(root); err == nil {
		t.Fatal("readManifest() accepted an invalid manifest")
	}
	writeParityFile(
		t,
		root,
		"benchmarks/spring-petclinic.json",
		`{`,
	)
	if _, err := readManifest(root); err == nil {
		t.Fatal("readManifest() accepted malformed JSON")
	}
	if _, err := readManifest(filepath.Join(root, "missing")); err == nil {
		t.Fatal("readManifest() accepted a missing root")
	}
}

func TestMeasureEditsRestoresSource(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	const original = "package fixture\n"
	writeParityFile(t, root, "fixture.go", original)
	values, err := measureEdits(
		context.Background(),
		root,
		"fixture.go",
		[]string{"go", "version"},
		1,
		2,
		commandRun,
	)
	if err != nil {
		t.Fatalf("measureEdits() error = %v", err)
	}
	if len(values) != 2 {
		t.Fatalf("measureEdits() returned %d samples", len(values))
	}
	content, err := os.ReadFile(filepath.Join(root, "fixture.go"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(content) != original {
		t.Fatalf("measureEdits() left source as %q", content)
	}
}

func TestMeasureEditsReportsFailures(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if _, err := measureEdits(
		context.Background(),
		root,
		"missing.go",
		[]string{"unused"},
		0,
		1,
		commandRun,
	); err == nil {
		t.Fatal("measureEdits() accepted a missing source")
	}
	writeParityFile(t, root, "fixture.go", "package fixture\n")
	sentinel := errors.New("build failed")
	if _, err := measureEdits(
		context.Background(),
		root,
		"fixture.go",
		[]string{"unused"},
		0,
		1,
		func(context.Context, string, string, ...string) error {
			return sentinel
		},
	); !errors.Is(err, sentinel) {
		t.Fatalf("measureEdits() error = %v, want %v", err, sentinel)
	}
}

func TestRunComparison(t *testing.T) {
	t.Parallel()

	repositoryRoot := t.TempDir()
	springRoot := t.TempDir()
	writeParityFile(
		t,
		filepath.Join(repositoryRoot, "examples", "petclinic"),
		"owner/controller.go",
		"package owner\n",
	)
	writeParityFile(t, springRoot, "OwnerController.java", "class OwnerController {}\n")
	spec := manifest{
		Schema:  "spice.spring-parity/v1",
		Samples: 2,
		Warmups: 1,
		Reference: reference{
			Commit: "pinned",
		},
		Scenarios: []scenario{{
			Name:            "body",
			SpiceSource:     "owner/controller.go",
			SpringSource:    "OwnerController.java",
			MaximumSpiceP90: 1000,
			MaximumP90Ratio: 1_000_000_000,
		}},
	}
	executions := 0
	execute := func(
		context.Context,
		string,
		string,
		...string,
	) error {
		executions++
		return nil
	}
	captureOutput := func(
		context.Context,
		string,
		string,
		...string,
	) (string, error) {
		return "pinned\n", nil
	}
	result, err := runComparison(
		context.Background(),
		repositoryRoot,
		springRoot,
		spec,
		execute,
		captureOutput,
	)
	if err != nil {
		t.Fatalf("runComparison() error = %v", err)
	}
	if result.Scenario != "body" ||
		len(result.SpiceMS) != 2 ||
		len(result.SpringMS) != 2 ||
		executions != 6 {
		t.Fatalf("runComparison() = %+v, executions %d", result, executions)
	}
}

func TestRunComparisonRejectsInputsAndBudgets(t *testing.T) {
	t.Parallel()

	repositoryRoot := t.TempDir()
	springRoot := t.TempDir()
	writeParityFile(
		t,
		filepath.Join(repositoryRoot, "examples", "petclinic"),
		"app.go",
		"package app\n",
	)
	writeParityFile(t, springRoot, "App.java", "class App {}\n")
	spec := manifest{
		Schema:  "spice.spring-parity/v1",
		Samples: 1,
		Reference: reference{
			Commit: "pinned",
		},
		Scenarios: []scenario{{
			Name:            "body",
			SpiceSource:     "app.go",
			SpringSource:    "App.java",
			MaximumSpiceP90: -1,
			MaximumP90Ratio: 1,
		}},
	}
	execute := func(
		context.Context,
		string,
		string,
		...string,
	) error {
		return nil
	}
	captureOutput := func(
		context.Context,
		string,
		string,
		...string,
	) (string, error) {
		return "wrong\n", nil
	}
	if _, err := runComparison(
		context.Background(),
		repositoryRoot,
		springRoot,
		spec,
		execute,
		captureOutput,
	); err == nil {
		t.Fatal("runComparison() accepted the wrong Spring commit")
	}
	captureOutput = func(
		context.Context,
		string,
		string,
		...string,
	) (string, error) {
		return "pinned\n", nil
	}
	if _, err := runComparison(
		context.Background(),
		repositoryRoot,
		springRoot,
		spec,
		execute,
		captureOutput,
	); err == nil {
		t.Fatal("runComparison() accepted a failed Spice budget")
	}

	sentinel := errors.New("capture unavailable")
	captureOutput = func(
		context.Context,
		string,
		string,
		...string,
	) (string, error) {
		return "", sentinel
	}
	if _, err := runComparison(
		context.Background(),
		repositoryRoot,
		springRoot,
		spec,
		execute,
		captureOutput,
	); !errors.Is(err, sentinel) {
		t.Fatalf("runComparison() capture error = %v", err)
	}
}

func TestIsSpiceRepositoryRoot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	found, err := isSpiceRepositoryRoot(root)
	if err != nil || found {
		t.Fatalf("isSpiceRepositoryRoot(empty) = %t, %v", found, err)
	}
	writeParityFile(
		t,
		root,
		"go.mod",
		"module github.com/spice-framework/spice\n",
	)
	found, err = isSpiceRepositoryRoot(root)
	if err != nil || !found {
		t.Fatalf("isSpiceRepositoryRoot(Spice) = %t, %v", found, err)
	}
}

func TestFindRepositoryRoot(t *testing.T) {
	root, err := findRepositoryRoot()
	if err != nil {
		t.Fatalf("findRepositoryRoot() error = %v", err)
	}
	want, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("Abs() error = %v", err)
	}
	if root != want {
		t.Fatalf("findRepositoryRoot() = %q, want %q", root, want)
	}
}

func TestCaptureAndCommandRun(t *testing.T) {
	t.Parallel()

	executable := "go"
	arguments := []string{"version"}
	if runtime.GOOS == "windows" {
		executable = "cmd"
		arguments = []string{"/c", "echo", "spice"}
	}
	output, err := capture(
		context.Background(),
		t.TempDir(),
		executable,
		arguments...,
	)
	if err != nil {
		t.Fatalf("capture() error = %v", err)
	}
	if strings.TrimSpace(output) == "" {
		t.Fatal("capture() returned empty output")
	}
	if err := commandRun(
		context.Background(),
		t.TempDir(),
		executable,
		arguments...,
	); err != nil {
		t.Fatalf("commandRun() error = %v", err)
	}
	if _, err := capture(
		context.Background(),
		t.TempDir(),
		"definitely-not-a-spice-command",
	); err == nil {
		t.Fatal("capture() accepted a missing executable")
	}
	if err := commandRun(
		context.Background(),
		t.TempDir(),
		"definitely-not-a-spice-command",
	); err == nil {
		t.Fatal("commandRun() accepted a missing executable")
	}
}

func writeParityFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}
