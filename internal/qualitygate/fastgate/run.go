// Package fastgate executes the repository's affected-package edit loop.
package fastgate

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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

	"github.com/spice-framework/spice/internal/qualitygate/affected"
)

const (
	requiredGoVersion = "go1.26.5"

	// MaximumGeneratedTargetLines is the repository-wide upper bound for one
	// target-wide generated Go unit. Source-mirror units are intentionally
	// excluded because their ownership is already one source file per unit.
	MaximumGeneratedTargetLines = 400
	maximumGeneratedTargetLines = MaximumGeneratedTargetLines
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

// CheckGeneratedTargetBoundaries requires every generated target and every
// file below it to be owned by that module's committed Spice manifest. It also
// rejects retired generated monoliths and oversized target-wide Go units.
func CheckGeneratedTargetBoundaries(root string) error {
	for _, moduleRoot := range []string{
		root,
		filepath.Join(root, "examples", "commerce"),
		filepath.Join(root, "examples", "petclinic"),
		filepath.Join(root, "testdata", "annotationapp"),
	} {
		if err := checkGeneratedModuleRoot(moduleRoot); err != nil {
			return err
		}
	}
	return nil
}

type generatedOwnershipManifest struct {
	Target struct {
		OutputDir string `json:"output_dir"`
	} `json:"target"`
	Files []struct {
		Path string `json:"path"`
	} `json:"files"`
}

func checkGeneratedModuleRoot(moduleRoot string) error {
	generatedRoot := filepath.Join(moduleRoot, "internal", "spicegen")
	entries, err := os.ReadDir(generatedRoot)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read generated root %s: %w", generatedRoot, err)
	}
	manifests, err := filepath.Glob(filepath.Join(moduleRoot, ".spice", "*.manifest.json"))
	if err != nil {
		return fmt.Errorf("find generated ownership manifests in %s: %w", moduleRoot, err)
	}
	ownedTargets := make(map[string]string, len(manifests))
	for _, manifestPath := range manifests {
		manifest, loadErr := loadGeneratedOwnershipManifest(moduleRoot, manifestPath)
		if loadErr != nil {
			return loadErr
		}
		targetPath := filepath.Join(moduleRoot, filepath.FromSlash(manifest.Target.OutputDir))
		ownedTargets[filepath.Clean(targetPath)] = manifestPath
		if checkErr := checkGeneratedTargetRoot(moduleRoot, targetPath, manifestPath, manifest); checkErr != nil {
			return checkErr
		}
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			return fmt.Errorf("%s is not a generated target directory", filepath.Join(generatedRoot, entry.Name()))
		}
		targetPath := filepath.Join(generatedRoot, entry.Name())
		if _, found := ownedTargets[filepath.Clean(targetPath)]; !found {
			containsFiles, inspectErr := directoryContainsFiles(targetPath)
			if inspectErr != nil {
				return inspectErr
			}
			if containsFiles {
				return fmt.Errorf("generated target %s has no ownership manifest", targetPath)
			}
		}
	}
	return nil
}

func directoryContainsFiles(root string) (bool, error) {
	containsFiles := false
	err := filepath.WalkDir(root, func(
		path string,
		entry fs.DirEntry,
		walkErr error,
	) error {
		if walkErr != nil {
			return walkErr
		}
		if path != root && !entry.IsDir() {
			containsFiles = true
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("inspect unowned generated target %s: %w", root, err)
	}
	return containsFiles, nil
}

func loadGeneratedOwnershipManifest(
	moduleRoot string,
	manifestPath string,
) (generatedOwnershipManifest, error) {
	content, err := readScopedFile(moduleRoot, manifestPath)
	if err != nil {
		return generatedOwnershipManifest{}, fmt.Errorf("read ownership manifest %s: %w", manifestPath, err)
	}
	var manifest generatedOwnershipManifest
	if err := json.Unmarshal(content, &manifest); err != nil {
		return generatedOwnershipManifest{}, fmt.Errorf("decode ownership manifest %s: %w", manifestPath, err)
	}
	outputDir := filepath.Clean(filepath.FromSlash(manifest.Target.OutputDir))
	wantParent := filepath.Join("internal", "spicegen")
	if filepath.IsAbs(outputDir) || filepath.Dir(outputDir) != wantParent {
		return generatedOwnershipManifest{}, fmt.Errorf(
			"ownership manifest %s has unsafe generated target %q",
			manifestPath,
			manifest.Target.OutputDir,
		)
	}
	targetPath := filepath.Join(moduleRoot, outputDir)
	if !pathWithin(moduleRoot, targetPath) {
		return generatedOwnershipManifest{}, fmt.Errorf(
			"ownership manifest %s target escapes module root",
			manifestPath,
		)
	}
	return manifest, nil
}

func checkGeneratedTargetRoot(
	moduleRoot string,
	targetPath string,
	manifestPath string,
	manifest generatedOwnershipManifest,
) (resultErr error) {
	owned, err := generatedOwnedFiles(
		moduleRoot,
		targetPath,
		manifestPath,
		manifest,
	)
	if err != nil {
		return err
	}
	if targetErr := requireGeneratedTarget(manifestPath, targetPath); targetErr != nil {
		return targetErr
	}
	targetRoot, err := os.OpenRoot(targetPath)
	if err != nil {
		return fmt.Errorf("open generated target %s: %w", targetPath, err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, targetRoot.Close())
	}()
	boundary := generatedTargetBoundary{
		targetPath:   targetPath,
		manifestPath: manifestPath,
		root:         targetRoot,
		owned:        owned,
		seen:         make(map[string]struct{}, len(owned)),
	}
	if err := filepath.WalkDir(targetPath, boundary.visit); err != nil {
		return err
	}
	for filePath := range boundary.owned {
		if _, found := boundary.seen[filePath]; !found {
			return fmt.Errorf("ownership manifest %s references missing file %s", manifestPath, filePath)
		}
	}
	return nil
}

func generatedOwnedFiles(
	moduleRoot string,
	targetPath string,
	manifestPath string,
	manifest generatedOwnershipManifest,
) (map[string]struct{}, error) {
	owned := make(map[string]struct{}, len(manifest.Files))
	for _, file := range manifest.Files {
		filePath := filepath.Clean(filepath.Join(moduleRoot, filepath.FromSlash(file.Path)))
		if !pathWithin(targetPath, filePath) {
			return nil, fmt.Errorf(
				"ownership manifest %s file %q is outside generated target %s",
				manifestPath,
				file.Path,
				targetPath,
			)
		}
		if _, duplicate := owned[filePath]; duplicate {
			return nil, fmt.Errorf("ownership manifest %s repeats file %q", manifestPath, file.Path)
		}
		owned[filePath] = struct{}{}
	}
	return owned, nil
}

func requireGeneratedTarget(manifestPath, targetPath string) error {
	if _, err := os.Stat(targetPath); errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("ownership manifest %s target %s does not exist", manifestPath, targetPath)
	} else if err != nil {
		return fmt.Errorf("inspect generated target %s: %w", targetPath, err)
	}
	return nil
}

type generatedTargetBoundary struct {
	targetPath   string
	manifestPath string
	root         *os.Root
	owned        map[string]struct{}
	seen         map[string]struct{}
}

func (boundary *generatedTargetBoundary) visit(
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
	cleanPath := filepath.Clean(filePath)
	if _, found := boundary.owned[cleanPath]; !found {
		return fmt.Errorf(
			"generated file %s is not owned by %s",
			filePath,
			boundary.manifestPath,
		)
	}
	boundary.seen[cleanPath] = struct{}{}
	relative, err := filepath.Rel(boundary.targetPath, filePath)
	if err != nil {
		return fmt.Errorf("resolve generated target path %s: %w", filePath, err)
	}
	if strings.Contains(filepath.ToSlash(relative), "/") {
		return nil
	}
	if entry.Name() == "zz_spice_gen.go" {
		return fmt.Errorf("%s is the retired generated target monolith", filePath)
	}
	if !strings.HasPrefix(entry.Name(), "spice_") ||
		!strings.HasSuffix(entry.Name(), "_gen.go") {
		return nil
	}
	lines, err := fileLineCount(boundary.root, relative)
	if err != nil {
		return err
	}
	if lines > MaximumGeneratedTargetLines {
		return fmt.Errorf(
			"%s has %d lines; generated target units must not exceed %d lines",
			filePath,
			lines,
			MaximumGeneratedTargetLines,
		)
	}
	return nil
}

func pathWithin(rootPath, candidatePath string) bool {
	relative, err := filepath.Rel(rootPath, candidatePath)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func readScopedFile(rootPath, filePath string) (result []byte, resultErr error) {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, fmt.Errorf("open scoped root %s: %w", rootPath, err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, root.Close())
	}()
	relative, err := filepath.Rel(rootPath, filePath)
	if err != nil {
		return nil, fmt.Errorf("resolve scoped file %s below %s: %w", filePath, rootPath, err)
	}
	if !pathWithin(rootPath, filePath) {
		return nil, fmt.Errorf("scoped file %s escapes %s", filePath, rootPath)
	}
	result, err = root.ReadFile(relative)
	if err != nil {
		return nil, fmt.Errorf("read scoped file %s: %w", filePath, err)
	}
	return result, nil
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
