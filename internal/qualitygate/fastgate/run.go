// Package fastgate executes the repository's affected-package edit loop.
package fastgate

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/StevenBuglione/spice/internal/qualitygate/affected"
)

const (
	requiredGoVersion           = "go1.26.5"
	maximumGeneratedTargetLines = 400
)

// Config describes one fast-gate invocation.
type Config struct {
	RepositoryRoot string
	Base           string
	Stdout         io.Writer
	BuildPlan      func(context.Context, affected.Config) (affected.Plan, error)
}

// Run executes deterministic affected formatting, generated-boundary, vet,
// and test checks.
func Run(ctx context.Context, config Config) error {
	writer := config.Stdout
	if writer == nil {
		writer = io.Discard
	}
	reporter := log.New(writer, "", 0)
	buildPlan := config.BuildPlan
	if buildPlan == nil {
		buildPlan = affected.Build
	}
	if err := checkGoVersion(ctx, config.RepositoryRoot); err != nil {
		return err
	}
	plan, err := buildPlan(ctx, affected.Config{
		RepositoryRoot: config.RepositoryRoot,
		Base:           config.Base,
	})
	if err != nil {
		return fmt.Errorf("plan affected verification: %w", err)
	}
	return runPlan(ctx, config.RepositoryRoot, reporter, plan)
}

func runPlan(
	ctx context.Context,
	root string,
	reporter *log.Logger,
	plan affected.Plan,
) error {
	reporter.Printf(
		"affected base %s: %d changed paths, %d module plans",
		plan.Base,
		len(plan.Changed),
		len(plan.Modules),
	)
	if plan.Empty() {
		reporter.Print("==> no affected work")
		return nil
	}
	for _, reason := range plan.Reasons {
		reporter.Printf("affected selection widened: %s", reason)
	}
	if plan.SpringCoverage {
		if err := step(reporter, "Spring coverage resolution", func() error {
			return checkSpringCoverage(root)
		}); err != nil {
			return err
		}
	}
	if err := step(reporter, "generated target boundaries", func() error {
		return CheckGeneratedTargetBoundaries(root)
	}); err != nil {
		return err
	}
	if len(plan.GoFiles) != 0 {
		if err := step(reporter, "affected formatting", func() error {
			return checkFormatted(ctx, root, plan.GoFiles)
		}); err != nil {
			return err
		}
	}
	for _, module := range plan.Modules {
		if err := runModule(ctx, reporter, root, module); err != nil {
			return err
		}
	}
	if plan.GoLand {
		reporter.Print("==> GoLand inputs changed; make goland remains the installed-IDE gate")
	}
	if plan.Zed {
		reporter.Print("==> Zed inputs changed; make zed remains the extension gate")
	}
	reporter.Print("==> affected verification passed")
	return nil
}

func runModule(
	ctx context.Context,
	reporter *log.Logger,
	repositoryRoot string,
	module affected.ModulePlan,
) error {
	if len(module.Packages) == 0 {
		return nil
	}
	displayRoot, err := filepath.Rel(repositoryRoot, module.Root)
	if err != nil {
		displayRoot = module.Root
	}
	for _, operation := range []string{"vet", "test"} {
		if err := step(
			reporter,
			"affected "+operation+" "+displayRoot,
			func() error {
				arguments := append(
					[]string{operation},
					module.Packages...,
				)
				return command(ctx, module.Root, "go", arguments...)
			},
		); err != nil {
			return err
		}
	}
	return nil
}

func step(reporter *log.Logger, name string, run func() error) error {
	started := time.Now()
	reporter.Printf("==> %s", name)
	if err := run(); err != nil {
		return fmt.Errorf(
			"%s (%s): %w",
			name,
			time.Since(started).Round(time.Millisecond),
			err,
		)
	}
	reporter.Printf(
		"<== %s passed in %s",
		name,
		time.Since(started).Round(time.Millisecond),
	)
	return nil
}

func checkGoVersion(ctx context.Context, root string) error {
	output, err := capture(ctx, root, "go", "version")
	if err != nil {
		return err
	}
	fields := strings.Fields(output)
	if len(fields) < 3 || fields[2] != requiredGoVersion {
		return fmt.Errorf(
			"go version is %q, require %s",
			strings.TrimSpace(output),
			requiredGoVersion,
		)
	}
	return nil
}

func checkFormatted(
	ctx context.Context,
	root string,
	names []string,
) error {
	files := make([]string, 0, len(names))
	for _, name := range names {
		relative := filepath.FromSlash(name)
		info, err := os.Stat(filepath.Join(root, relative))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect affected Go file %s: %w", name, err)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("affected Go path %s is not a regular file", name)
		}
		files = append(files, relative)
	}
	if len(files) == 0 {
		return nil
	}
	for _, tool := range []string{"goimports", "gofumpt"} {
		path, err := toolPath(ctx, root, tool)
		if err != nil {
			return err
		}
		var unformatted []string
		for batch := range slices.Chunk(files, 100) {
			arguments := append([]string{"-l"}, batch...)
			output, err := capture(ctx, root, path, arguments...)
			if err != nil {
				return err
			}
			if trimmed := strings.TrimSpace(output); trimmed != "" {
				unformatted = append(
					unformatted,
					strings.Split(trimmed, "\n")...,
				)
			}
		}
		if len(unformatted) != 0 {
			return fmt.Errorf(
				"%s: files need formatting:\n%s",
				tool,
				strings.Join(unformatted, "\n"),
			)
		}
	}
	return nil
}

func toolPath(ctx context.Context, root, name string) (string, error) {
	output, err := capture(ctx, root, "go", "tool", "-C", "tools", "-n", name)
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(output)
	if path == "" {
		return "", fmt.Errorf("resolve tool %q: empty path", name)
	}
	return path, nil
}

func command(
	ctx context.Context,
	root string,
	executable string,
	arguments ...string,
) error {
	// #nosec G204 -- executable is either the fixed Go tool or a path returned
	// by `go tool -n`; arguments are passed directly without a command shell.
	process := exec.CommandContext(ctx, executable, arguments...)
	process.Dir = root
	process.Stdout = os.Stdout
	process.Stderr = os.Stderr
	if err := process.Run(); err != nil {
		return fmt.Errorf(
			"%s %s: %w",
			executable,
			strings.Join(arguments, " "),
			err,
		)
	}
	return nil
}

func capture(
	ctx context.Context,
	root string,
	executable string,
	arguments ...string,
) (string, error) {
	// #nosec G204 -- see command; no shell interprets executable or arguments.
	process := exec.CommandContext(ctx, executable, arguments...)
	process.Dir = root
	output, err := process.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf(
			"%s %s: %w\n%s",
			executable,
			strings.Join(arguments, " "),
			err,
			strings.TrimSpace(string(output)),
		)
	}
	return string(output), nil
}

// CheckGeneratedTargetBoundaries rejects retired generated monoliths and
// oversized target-wide generated units.
func CheckGeneratedTargetBoundaries(root string) error {
	for _, generatedRoot := range []string{
		filepath.Join(root, "internal", "spicegen"),
		filepath.Join(root, "examples", "petclinic", "internal", "spicegen"),
		filepath.Join(root, "testdata", "annotationapp", "internal", "spicegen"),
	} {
		if err := checkGeneratedTargetRoot(generatedRoot); err != nil {
			return err
		}
	}
	return nil
}

func checkGeneratedTargetRoot(rootPath string) (resultErr error) {
	root, err := os.OpenRoot(rootPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open generated target root %s: %w", rootPath, err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, root.Close())
	}()
	return filepath.WalkDir(rootPath, func(
		filePath string,
		entry fs.DirEntry,
		walkErr error,
	) error {
		if walkErr != nil {
			if errors.Is(walkErr, fs.ErrNotExist) {
				return nil
			}
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(rootPath, filePath)
		if err != nil {
			return fmt.Errorf("resolve generated target path %s: %w", filePath, err)
		}
		if len(strings.Split(filepath.ToSlash(relative), "/")) != 2 {
			return nil
		}
		name := entry.Name()
		if name == "zz_spice_gen.go" {
			return fmt.Errorf("%s is the retired generated target monolith", filePath)
		}
		if !strings.HasPrefix(name, "spice_") ||
			!strings.HasSuffix(name, "_gen.go") {
			return nil
		}
		lines, err := fileLineCount(root, relative)
		if err != nil {
			return err
		}
		if lines > maximumGeneratedTargetLines {
			return fmt.Errorf(
				"%s has %d lines; generated target units must not exceed %d lines",
				filePath,
				lines,
				maximumGeneratedTargetLines,
			)
		}
		return nil
	})
}

func fileLineCount(root *os.Root, name string) (result int, resultErr error) {
	file, err := root.Open(name)
	if err != nil {
		return 0, fmt.Errorf("open generated target unit %s: %w", name, err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, file.Close())
	}()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		result++
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("read generated target unit %s: %w", name, err)
	}
	return result, nil
}

func checkSpringCoverage(root string) (resultErr error) {
	repository, err := os.OpenRoot(root)
	if err != nil {
		return fmt.Errorf("open repository root: %w", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, repository.Close())
	}()
	content, err := repository.ReadFile("docs/spring-coverage.md")
	if err != nil {
		return fmt.Errorf("read Spring coverage map: %w", err)
	}
	allowed := map[string]struct{}{
		"available":   {},
		"integration": {},
		"not-planned": {},
	}
	rows := 0
	scanner := bufio.NewScanner(bytes.NewReader(content))
	for line := 1; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(text, "|") ||
			strings.HasPrefix(text, "|---") ||
			strings.HasPrefix(text, "| Area ") {
			continue
		}
		columns := strings.Split(text, "|")
		if len(columns) != 6 {
			return fmt.Errorf(
				"spring coverage map line %d has %d columns, want 4",
				line,
				len(columns)-2,
			)
		}
		status := strings.TrimSpace(columns[4])
		if _, valid := allowed[status]; !valid {
			return fmt.Errorf(
				"spring coverage map line %d has unresolved or invalid status %q",
				line,
				status,
			)
		}
		if status == "not-planned" &&
			!strings.Contains(
				strings.ToLower(columns[3]),
				"deliberately",
			) {
			return fmt.Errorf(
				"spring coverage map line %d marks not-planned without a deliberate rationale",
				line,
			)
		}
		rows++
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan Spring coverage map: %w", err)
	}
	if rows == 0 {
		return errors.New("spring coverage map has no capability rows")
	}
	return nil
}
