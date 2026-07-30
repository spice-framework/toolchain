// Command qualitygate runs Spice's repository-owned, cross-platform verification.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/StevenBuglione/spice/internal/bootstrapcheck"
)

const (
	requiredGoVersion              = "go1.26.5"
	requiredRustVersion            = "1.93.0"
	requiredGradleVersion          = "9.6.1"
	requiredGradleDistributionHash = "9c0f7faeeb306cb14e4279a3e084ca6b596894089a0638e68a07c945a32c9e14"
	requiredGradleWrapperHash      = "497c8c2a7e5031f6aa847f88104aa80a93532ec32ee17bdb8d1d2f67a194a9c7"
	minimumCoverage                = 85.0
	maximumGeneratedTargetLines    = 400
	modulePath                     = "github.com/StevenBuglione/spice"
)

var output = log.New(os.Stdout, "", 0)

var (
	runExternal        = command
	captureExternal    = capture
	checkGoLandWrapper = validateGoLandWrapper
	runBootstrapCheck  = bootstrapcheck.Run
)

func main() {
	os.Exit(execute()) // Entrypoint exception: propagate a failed verification to make and CI.
}

func execute() int {
	mode := flag.String("mode", "verify", "verification mode: benchmark, bootstrap, check, coverage, fmt, fuzz, goland, lint, security, smoke, test, vet, offline, zed, verify, or verify-release")
	flag.Parse()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	if err := run(ctx, *mode); err != nil {
		log.Printf("quality gate failed: %v", err)
		return 1
	}
	return 0
}

func run(ctx context.Context, mode string) error {
	root, err := repositoryRoot()
	if err != nil {
		return err
	}
	if err := checkGoVersion(ctx, root); err != nil {
		return err
	}

	modes := map[string]func() error{
		"benchmark": func() error { return benchmark(ctx, root) },
		"bootstrap": func() error {
			return runStep(verificationStep{
				name: "bootstrap and recovery",
				run:  func() error { return runBootstrapCheck(ctx, root) },
			})
		},
		"check":          func() error { return check(ctx, root) },
		"coverage":       func() error { return coverage(ctx, root) },
		"fmt":            func() error { return format(ctx, root, true) },
		"fuzz":           func() error { return fuzz(ctx, root) },
		"goland":         func() error { return goland(ctx, root) },
		"lint":           func() error { return lint(ctx, root) },
		"security":       func() error { return security(ctx, root) },
		"smoke":          func() error { return smoke(ctx, root) },
		"test":           func() error { return test(ctx, root) },
		"vet":            func() error { return runExternal(ctx, root, nil, "go", "vet", "./...") },
		"offline":        func() error { return offline(ctx, root) },
		"zed":            func() error { return zed(ctx, root) },
		"verify":         func() error { return verify(ctx, root, false) },
		"verify-release": func() error { return verify(ctx, root, true) },
	}
	runMode, found := modes[mode]
	if !found {
		return fmt.Errorf("unknown mode %q", mode)
	}
	return runMode()
}

type verificationStep struct {
	name string
	run  func() error
}

func check(ctx context.Context, root string) error {
	return runSequential([]verificationStep{
		{"Spring coverage resolution", func() error {
			return checkSpringCoverage(root)
		}},
		{"generated target boundaries", func() error {
			return checkGeneratedTargetBoundaries(root)
		}},
		{"formatting", func() error { return format(ctx, root, false) }},
		{"module tidiness", func() error { return checkModuleTidy(ctx, root) }},
		{"go vet", func() error { return vet(ctx, root) }},
		{"lint and nil safety", func() error { return lint(ctx, root) }},
		{"all-package test compilation", func() error {
			return compileTests(ctx, root)
		}},
	})
}

func petclinicRoot(root string) string {
	return filepath.Join(root, "examples", "petclinic")
}

func vet(ctx context.Context, root string) error {
	for _, directory := range []string{root, petclinicRoot(root)} {
		if err := runExternal(
			ctx,
			directory,
			nil,
			"go",
			"vet",
			"./...",
		); err != nil {
			return err
		}
	}
	return nil
}

func compileTests(ctx context.Context, root string) error {
	for _, directory := range []string{root, petclinicRoot(root)} {
		if err := runExternal(
			ctx,
			directory,
			nil,
			"go",
			"test",
			"-run=^$",
			"./...",
		); err != nil {
			return err
		}
	}
	return nil
}

func verify(ctx context.Context, root string, release bool) error {
	if err := runSequential([]verificationStep{
		{"Spring coverage resolution", func() error {
			return checkSpringCoverage(root)
		}},
		{"generated target boundaries", func() error {
			return checkGeneratedTargetBoundaries(root)
		}},
		{"formatting", func() error { return format(ctx, root, false) }},
		{"module tidiness", func() error { return checkModuleTidy(ctx, root) }},
		{"vendor consistency", func() error { return checkVendor(ctx, root) }},
	}); err != nil {
		return err
	}
	parallelSteps := []verificationStep{
		{"go vet", func() error { return vet(ctx, root) }},
		{"lint and nil safety", func() error { return lint(ctx, root) }},
		{"security", func() error { return security(ctx, root) }},
		{"Zed extension", func() error { return zed(ctx, root) }},
	}
	if err := runParallel(parallelSteps, 4); err != nil {
		return err
	}
	runGoLand := release
	if !runGoLand {
		var err error
		runGoLand, err = goLandAffected(ctx, root)
		if err != nil {
			return fmt.Errorf("determine GoLand verification scope: %w", err)
		}
	}
	if runGoLand {
		if err := runStep(verificationStep{
			name: "GoLand plugin",
			run:  func() error { return goland(ctx, root) },
		}); err != nil {
			return err
		}
	} else {
		output.Println("==> GoLand plugin skipped: no editor/compiler/LSP inputs changed")
	}
	if release {
		if err := runStep(verificationStep{
			name: "benchmark budgets",
			run:  func() error { return benchmark(ctx, root) },
		}); err != nil {
			return err
		}
	}
	// These stages each perform broad Go compilation. Running them together
	// oversubscribes compiler processes and makes the wall clock worse on
	// developer workstations, so they intentionally reuse one another's cache.
	if err := runSequential([]verificationStep{
		{"tests", func() error { return test(ctx, root) }},
		{"fuzz smoke tests", func() error { return fuzz(ctx, root) }},
		{"coverage", func() error { return coverage(ctx, root) }},
		{"offline vendor tests", func() error { return offline(ctx, root) }},
		{"executable smoke tests", func() error { return smoke(ctx, root) }},
		{"bootstrap and recovery", func() error {
			return runBootstrapCheck(ctx, root)
		}},
	}); err != nil {
		return err
	}
	output.Println("==> all verification passed")
	return nil
}

func checkGeneratedTargetBoundaries(root string) error {
	generatedRoots := []string{
		filepath.Join(root, "internal", "spicegen"),
		filepath.Join(root, "examples", "petclinic", "internal", "spicegen"),
		filepath.Join(
			root,
			"testdata",
			"annotationapp",
			"internal",
			"spicegen",
		),
	}
	for _, generatedRoot := range generatedRoots {
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
			return fmt.Errorf(
				"%s is the retired generated target monolith",
				filePath,
			)
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

func fileLineCount(
	root *os.Root,
	relativePath string,
) (result int, resultErr error) {
	file, err := root.Open(relativePath)
	if err != nil {
		return 0, fmt.Errorf(
			"open generated target unit %s: %w",
			relativePath,
			err,
		)
	}
	defer func() {
		resultErr = errors.Join(resultErr, file.Close())
	}()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		result++
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf(
			"read generated target unit %s: %w",
			relativePath,
			err,
		)
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
	file, err := repository.Open("docs/spring-coverage.md")
	if err != nil {
		return fmt.Errorf("open Spring coverage map: %w", err)
	}
	content, err := io.ReadAll(file)
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		return fmt.Errorf(
			"read Spring coverage map: %w",
			errors.Join(err, closeErr),
		)
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

func goLandAffected(ctx context.Context, root string) (bool, error) {
	tracked, err := captureExternal(
		ctx,
		root,
		"git",
		"diff",
		"--name-only",
		"origin/main",
		"--",
	)
	if err != nil {
		return false, err
	}
	untracked, err := captureExternal(
		ctx,
		root,
		"git",
		"ls-files",
		"--others",
		"--exclude-standard",
	)
	if err != nil {
		return false, err
	}
	paths := append(
		strings.Fields(tracked),
		strings.Fields(untracked)...,
	)
	return requiresGoLand(paths), nil
}

func requiresGoLand(paths []string) bool {
	exact := map[string]struct{}{
		"go.mod":      {},
		"go.sum":      {},
		"go.work":     {},
		"go.work.sum": {},
	}
	prefixes := []string{
		"annotation/",
		"cmd/spice/",
		"compiler/",
		"editors/goland/",
		"examples/commerce/",
		"internal/lsp/",
		"vendor/",
	}
	for _, name := range paths {
		name = strings.TrimPrefix(
			filepath.ToSlash(strings.TrimSpace(name)),
			"./",
		)
		if _, found := exact[name]; found {
			return true
		}
		for _, prefix := range prefixes {
			if strings.HasPrefix(name, prefix) {
				return true
			}
		}
	}
	return false
}

func runSequential(steps []verificationStep) error {
	for _, step := range steps {
		if err := runStep(step); err != nil {
			return err
		}
	}
	return nil
}

func runParallel(steps []verificationStep, workers int) error {
	if workers < 1 {
		return errors.New("run verification stages: workers must be positive")
	}
	results := make([]error, len(steps))
	limiter := make(chan struct{}, workers)
	var wait sync.WaitGroup
	for index, step := range steps {
		wait.Go(func() {
			limiter <- struct{}{}
			defer func() { <-limiter }()
			results[index] = runStep(step)
		})
	}
	wait.Wait()
	for _, err := range results {
		if err != nil {
			return err
		}
	}
	return nil
}

func runStep(step verificationStep) error {
	started := time.Now()
	output.Printf("==> %s", step.name)
	if err := step.run(); err != nil {
		return fmt.Errorf("%s (%s): %w", step.name, time.Since(started).Round(time.Millisecond), err)
	}
	output.Printf("<== %s passed in %s", step.name, time.Since(started).Round(time.Millisecond))
	return nil
}

func goland(ctx context.Context, root string) error {
	golandRoot := filepath.Join(root, "editors", "goland")
	if err := checkGoLandWrapper(golandRoot); err != nil {
		return err
	}
	executable := filepath.Join(golandRoot, "gradlew")
	if runtime.GOOS == "windows" {
		executable += ".bat"
	}
	arguments := []string{"--no-daemon", "--console=plain"}
	localPath, err := localGoLandPath()
	if err != nil {
		return err
	}
	if localPath != "" {
		arguments = append(arguments, "-PgolandPath="+localPath)
	}
	arguments = append(arguments, "test", "buildPlugin")
	if runtime.GOOS != "darwin" {
		arguments = append(arguments, "integrationTest")
	}
	arguments = append(
		arguments,
		"verifyPluginProjectConfiguration",
		"verifyPluginStructure",
		"verifyPlugin",
	)
	if runtime.GOOS == "linux" &&
		strings.TrimSpace(os.Getenv("DISPLAY")) == "" {
		arguments = append([]string{"-a", executable}, arguments...)
		executable = "xvfb-run"
	}
	return runExternal(ctx, golandRoot, nil, executable, arguments...)
}

func validateGoLandWrapper(golandRoot string) error {
	properties, err := readRootFile(
		golandRoot,
		filepath.Join("gradle", "wrapper", "gradle-wrapper.properties"),
	)
	if err != nil {
		return fmt.Errorf("read GoLand Gradle wrapper properties: %w", err)
	}
	for _, expected := range []string{
		"distributionUrl=https\\://services.gradle.org/distributions/gradle-" +
			requiredGradleVersion + "-bin.zip",
		"distributionSha256Sum=" + requiredGradleDistributionHash,
	} {
		if !strings.Contains(string(properties), expected) {
			return fmt.Errorf(
				"GoLand Gradle wrapper properties do not contain %q",
				expected,
			)
		}
	}
	wrapper, err := readRootFile(
		golandRoot,
		filepath.Join("gradle", "wrapper", "gradle-wrapper.jar"),
	)
	if err != nil {
		return fmt.Errorf("read GoLand Gradle wrapper JAR: %w", err)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(wrapper))
	if hash != requiredGradleWrapperHash {
		return fmt.Errorf(
			"GoLand Gradle wrapper JAR checksum is %s, require %s",
			hash,
			requiredGradleWrapperHash,
		)
	}
	for _, script := range []string{"gradlew", "gradlew.bat"} {
		if _, err := readRootFile(golandRoot, script); err != nil {
			return fmt.Errorf("read GoLand %s script: %w", script, err)
		}
	}
	return nil
}

func localGoLandPath() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("SPICE_GOLAND_HOME")); configured != "" {
		// #nosec G703 -- the verifier intentionally performs a read-only stat
		// on the user-selected IDE installation; it never reads a child path.
		info, err := os.Stat(configured)
		if err != nil {
			return "", fmt.Errorf("inspect SPICE_GOLAND_HOME %q: %w", configured, err)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("SPICE_GOLAND_HOME %q is not a directory", configured)
		}
		return filepath.Clean(configured), nil
	}
	var candidate string
	switch runtime.GOOS {
	case "windows":
		if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
			candidate = filepath.Join(localAppData, "Programs", "GoLand")
		}
	case "darwin":
		candidate = filepath.Join(
			string(filepath.Separator),
			"Applications",
			"GoLand.app",
			"Contents",
		)
	}
	if candidate == "" {
		return "", nil
	}
	// #nosec G703 -- this read-only stat targets one fixed GoLand location
	// below the current user's platform-owned application directory.
	info, err := os.Stat(candidate)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("inspect local GoLand path %q: %w", candidate, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("local GoLand path %q is not a directory", candidate)
	}
	return filepath.Clean(candidate), nil
}

func repositoryRoot() (string, error) {
	current, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}
	for {
		data, readErr := readRootFile(current, "go.mod")
		if readErr == nil && bytes.Contains(data, []byte("module "+modulePath)) {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", errors.New("find Spice repository root: go.mod not found")
		}
		current = parent
	}
}

func readRootFile(directory, name string) ([]byte, error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	data, readErr := root.ReadFile(name)
	closeErr := root.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close root %q: %w", directory, closeErr)
	}
	return data, nil
}

func checkGoVersion(ctx context.Context, root string) error {
	stdout, err := captureExternal(ctx, root, "go", "version")
	if err != nil {
		return err
	}
	fields := strings.Fields(stdout)
	if len(fields) < 3 || fields[2] != requiredGoVersion {
		return fmt.Errorf("go version is %q, require %s", strings.TrimSpace(stdout), requiredGoVersion)
	}
	return nil
}

func checkRustVersion(ctx context.Context, root string) error {
	stdout, err := captureExternal(ctx, root, "rustc", "--version")
	if err != nil {
		return err
	}
	fields := strings.Fields(stdout)
	if len(fields) < 2 || fields[1] != requiredRustVersion {
		return fmt.Errorf(
			"rustc version is %q, require %s",
			strings.TrimSpace(stdout),
			requiredRustVersion,
		)
	}
	return nil
}

func zed(ctx context.Context, root string) error {
	zedRoot := filepath.Join(root, "editors", "zed")
	if err := checkRustVersion(ctx, zedRoot); err != nil {
		return err
	}
	commands := [][]string{
		{"fmt", "--check"},
		{"test", "--locked"},
		{"clippy", "--locked", "--all-targets", "--", "-D", "warnings"},
		{"build", "--locked", "--release", "--target", "wasm32-wasip2"},
	}
	for _, arguments := range commands {
		if err := runExternal(
			ctx,
			zedRoot,
			nil,
			"cargo",
			arguments...,
		); err != nil {
			return err
		}
	}
	fixtureRoot := filepath.Join(zedRoot, "fixture")
	if err := runExternal(
		ctx,
		fixtureRoot,
		nil,
		"go",
		"mod",
		"tidy",
		"-diff",
	); err != nil {
		return err
	}
	temp, err := os.MkdirTemp("", "spice-zed-*")
	if err != nil {
		return fmt.Errorf("create Zed fixture directory: %w", err)
	}
	defer removeTemporaryDirectory(temp)
	executable := filepath.Join(temp, "spice")
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	if err := runExternal(
		ctx,
		root,
		nil,
		"go",
		"build",
		"-trimpath",
		"-o",
		executable,
		"./cmd/spice",
	); err != nil {
		return err
	}
	if err := runExternal(
		ctx,
		fixtureRoot,
		nil,
		executable,
		"verify",
		"--format=json",
		"./...",
	); err != nil {
		return err
	}
	return nil
}

func format(ctx context.Context, root string, write bool) error {
	files, err := goFiles(root)
	if err != nil {
		return err
	}
	goimports, err := toolPath(ctx, root, "goimports")
	if err != nil {
		return err
	}
	gofumpt, err := toolPath(ctx, root, "gofumpt")
	if err != nil {
		return err
	}
	if write {
		if err := fileBatches(ctx, root, goimports, "-w", files); err != nil {
			return err
		}
		return fileBatches(ctx, root, gofumpt, "-w", files)
	}
	if err := checkFormatted(ctx, root, goimports, files); err != nil {
		return fmt.Errorf("goimports: %w", err)
	}
	if err := checkFormatted(ctx, root, gofumpt, files); err != nil {
		return fmt.Errorf("gofumpt: %w", err)
	}
	return nil
}

func goFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", ".tools", "vendor":
				if path != root {
					return filepath.SkipDir
				}
			}
			if path != root {
				relative, err := filepath.Rel(root, path)
				if err != nil {
					return fmt.Errorf(
						"make directory %q relative to repository: %w",
						path,
						err,
					)
				}
				if slices.Contains([]string{
					"bin",
					"dist",
					"out",
					filepath.Join("editors", "goland", ".gradle"),
					filepath.Join("editors", "goland", ".intellijPlatform"),
					filepath.Join("editors", "goland", "build"),
					filepath.Join("editors", "zed", "target"),
				}, relative) {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if filepath.Ext(entry.Name()) == ".go" {
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return fmt.Errorf("make %q relative to repository: %w", path, err)
			}
			files = append(files, relative)
		}
		return nil
	})
	slices.Sort(files)
	return files, err
}

func checkFormatted(ctx context.Context, root, formatter string, files []string) error {
	var unformatted []string
	for batch := range slices.Chunk(files, 100) {
		args := append([]string{"-l"}, batch...)
		stdout, err := captureExternal(ctx, root, formatter, args...)
		if err != nil {
			return err
		}
		if trimmed := strings.TrimSpace(stdout); trimmed != "" {
			unformatted = append(unformatted, strings.Split(trimmed, "\n")...)
		}
	}
	if len(unformatted) != 0 {
		return fmt.Errorf("files need formatting:\n%s", strings.Join(unformatted, "\n"))
	}
	return nil
}

func fileBatches(ctx context.Context, root, executable, option string, files []string) error {
	for batch := range slices.Chunk(files, 100) {
		args := append([]string{option}, batch...)
		if err := runExternal(ctx, root, nil, executable, args...); err != nil {
			return err
		}
	}
	return nil
}

func checkModuleTidy(ctx context.Context, root string) error {
	for _, directory := range []string{
		root,
		filepath.Join(root, "tools"),
		petclinicRoot(root),
	} {
		stdout, err := captureExternal(ctx, directory, "go", "mod", "tidy", "-diff")
		if err != nil {
			return err
		}
		if strings.TrimSpace(stdout) != "" {
			return fmt.Errorf("%s is not tidy:\n%s", directory, stdout)
		}
	}
	return nil
}

func checkVendor(ctx context.Context, root string) error {
	for _, directory := range []string{root, petclinicRoot(root)} {
		if err := checkModuleVendor(ctx, directory); err != nil {
			return err
		}
	}
	return nil
}

func checkModuleVendor(ctx context.Context, moduleRoot string) error {
	temp, err := os.MkdirTemp("", "spice-vendor-*")
	if err != nil {
		return fmt.Errorf("create temporary vendor directory: %w", err)
	}
	defer removeTemporaryDirectory(temp)

	generated := filepath.Join(temp, "vendor")
	if commandErr := runExternal(
		ctx,
		moduleRoot,
		nil,
		"go",
		"mod",
		"vendor",
		"-o",
		generated,
	); commandErr != nil {
		return commandErr
	}
	want, err := treeDigest(generated)
	if err != nil {
		return err
	}
	got, err := treeDigest(filepath.Join(moduleRoot, "vendor"))
	if err != nil {
		return err
	}
	if !equalDigests(want, got) {
		return fmt.Errorf(
			"vendor directory is stale in %s; run go mod vendor",
			moduleRoot,
		)
	}
	return nil
}

func treeDigest(root string) (map[string][sha256.Size]byte, error) {
	digests := make(map[string][sha256.Size]byte)
	directory, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("open root %q: %w", root, err)
	}
	rootFS := directory.FS()
	walkErr := fs.WalkDir(rootFS, ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(rootFS, path)
		if err != nil {
			return fmt.Errorf("read %q: %w", path, err)
		}
		digests[path] = sha256.Sum256(data)
		return nil
	})
	closeErr := directory.Close()
	if walkErr != nil {
		return nil, walkErr
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close root %q: %w", root, closeErr)
	}
	return digests, nil
}

func equalDigests(left, right map[string][sha256.Size]byte) bool {
	if len(left) != len(right) {
		return false
	}
	for path, digest := range left {
		if right[path] != digest {
			return false
		}
	}
	return true
}

func lint(ctx context.Context, root string) error {
	golangci, err := toolPath(ctx, root, "golangci-lint")
	if err != nil {
		return err
	}
	if commandErr := runExternal(ctx, root, nil, golangci, "run", "--timeout=10m"); commandErr != nil {
		return commandErr
	}
	if commandErr := runExternal(
		ctx,
		petclinicRoot(root),
		nil,
		golangci,
		"run",
		"--timeout=10m",
	); commandErr != nil {
		return commandErr
	}
	nilaway, err := toolPath(ctx, root, "nilaway")
	if err != nil {
		return err
	}
	if commandErr := runExternal(
		ctx,
		root,
		nil,
		nilaway,
		"-include-pkgs="+modulePath,
		"./...",
	); commandErr != nil {
		return commandErr
	}
	return runExternal(
		ctx,
		petclinicRoot(root),
		nil,
		nilaway,
		"-include-pkgs="+modulePath+"/examples/petclinic",
		"./...",
	)
}

func security(ctx context.Context, root string) error {
	gosec, err := toolPath(ctx, root, "gosec")
	if err != nil {
		return err
	}
	if commandErr := runExternal(ctx, root, nil, gosec, "-quiet", "-exclude-generated", "./..."); commandErr != nil {
		return commandErr
	}
	if commandErr := runExternal(
		ctx,
		petclinicRoot(root),
		nil,
		gosec,
		"-quiet",
		"-exclude-generated",
		"./...",
	); commandErr != nil {
		return commandErr
	}
	govulncheck, err := toolPath(ctx, root, "govulncheck")
	if err != nil {
		return err
	}
	if commandErr := runExternal(ctx, root, nil, govulncheck, "./..."); commandErr != nil {
		return commandErr
	}
	return runExternal(
		ctx,
		petclinicRoot(root),
		nil,
		govulncheck,
		"./...",
	)
}

func test(ctx context.Context, root string) error {
	for _, directory := range []string{root, petclinicRoot(root)} {
		if err := runExternal(
			ctx,
			directory,
			nil,
			"go",
			"test",
			"-shuffle=on",
			"-count=1",
			"./...",
		); err != nil {
			return err
		}
		if err := runExternal(
			ctx,
			directory,
			nil,
			"go",
			"test",
			"-race",
			"-shuffle=on",
			"-count=1",
			"./...",
		); err != nil {
			return err
		}
	}
	return nil
}

func fuzz(ctx context.Context, root string) error {
	targets := []struct {
		pkg    string
		target string
	}{
		{"./compiler/diagnostic", "FuzzDiagnosticReportJSON"},
		{"./compiler/parser", "FuzzParseComment"},
		{"./compiler/service", "FuzzNormalizeOverlay"},
		{"./compiler/validate", "FuzzOccurrences"},
		{"./internal/lsp", "FuzzRPCReader"},
		{"./config", "FuzzDecodeJSONObject"},
		{"./config", "FuzzResolveScalars"},
		{"./web", "FuzzDecodeJSON"},
		{"./httpclient", "FuzzResolveReference"},
		{"./mail", "FuzzNewMessage"},
		{"./mail/mailtest", "FuzzSnapshotMIME"},
		{"./messaging", "FuzzNewMessage"},
		{"./session", "FuzzManagerLoad"},
		{"./starter", "FuzzParseManifest"},
		{"./view", "FuzzRendererEscaping"},
	}
	for _, target := range targets {
		if err := runExternal(
			ctx,
			root,
			nil,
			"go",
			"test",
			target.pkg,
			"-run=^$",
			"-fuzz="+target.target,
			"-fuzztime=1s",
		); err != nil {
			return err
		}
	}
	return nil
}

func coverage(
	ctx context.Context,
	root string,
) (returnErr error) {
	temp, err := os.MkdirTemp("", "spice-coverage-*")
	if err != nil {
		return fmt.Errorf("create coverage directory: %w", err)
	}
	defer removeTemporaryDirectory(temp)

	coverageRoot, err := os.OpenRoot(temp)
	if err != nil {
		return fmt.Errorf("open coverage directory: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, coverageRoot.Close())
	}()
	const profileName = "coverage.out"
	const productProfileName = "coverage-product.out"
	profile := filepath.Join(temp, profileName)
	if commandErr := runExternal(ctx, root, nil, "go", "test", "-covermode=atomic", "-coverprofile="+profile, "./..."); commandErr != nil {
		return commandErr
	}
	if filterErr := filterGeneratedCoverageProfile(
		root,
		coverageRoot,
		profileName,
		productProfileName,
	); filterErr != nil {
		return filterErr
	}
	stdout, err := captureExternal(
		ctx,
		root,
		"go",
		"tool",
		"cover",
		"-func="+filepath.Join(temp, productProfileName),
	)
	if err != nil {
		return err
	}
	total, err := totalCoverage(stdout)
	if err != nil {
		return err
	}
	output.Printf("coverage: %.1f%% (minimum %.1f%%)", total, minimumCoverage)
	if total < minimumCoverage {
		return fmt.Errorf("repository coverage %.1f%% is below %.1f%%", total, minimumCoverage)
	}
	return runExternal(
		ctx,
		petclinicRoot(root),
		nil,
		"go",
		"test",
		"-covermode=atomic",
		"./...",
	)
}

func filterGeneratedCoverageProfile(
	root string,
	profileRoot *os.Root,
	profileName string,
	productProfileName string,
) (returnErr error) {
	content, err := profileRoot.ReadFile(profileName)
	if err != nil {
		return fmt.Errorf("read coverage profile: %w", err)
	}
	sourceRoot, err := os.OpenRoot(root)
	if err != nil {
		return fmt.Errorf("open coverage source root: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, sourceRoot.Close())
	}()
	var filtered bytes.Buffer
	scanner := bufio.NewScanner(bytes.NewReader(content))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "mode: ") ||
			!coverageLineUsesGeneratedSource(sourceRoot, line) {
			filtered.WriteString(line)
			filtered.WriteByte('\n')
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan coverage profile: %w", err)
	}
	if err := profileRoot.WriteFile(
		productProfileName,
		filtered.Bytes(),
		0o600,
	); err != nil {
		return fmt.Errorf("write product coverage profile: %w", err)
	}
	return nil
}

func coverageLineUsesGeneratedSource(root *os.Root, line string) bool {
	sourceID, _, found := strings.Cut(line, ":")
	if !found {
		return false
	}
	modulePrefix := modulePath + "/"
	if !strings.HasPrefix(sourceID, modulePrefix) {
		return false
	}
	sourcePath := filepath.FromSlash(
		strings.TrimPrefix(sourceID, modulePrefix),
	)
	content, err := root.ReadFile(sourcePath)
	if err != nil {
		return false
	}
	return hasCanonicalSpiceGeneratedMarker(content)
}

func hasCanonicalSpiceGeneratedMarker(content []byte) bool {
	const marker = "// Code generated by Spice. DO NOT EDIT."
	scanner := bufio.NewScanner(bytes.NewReader(content))
	for line := 0; line < 5 && scanner.Scan(); line++ {
		if scanner.Text() == marker {
			return true
		}
	}
	return false
}

func totalCoverage(report string) (float64, error) {
	scanner := bufio.NewScanner(strings.NewReader(report))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) >= 3 && fields[0] == "total:" {
			return strconv.ParseFloat(strings.TrimSuffix(fields[len(fields)-1], "%"), 64)
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("read coverage report: %w", err)
	}
	return 0, errors.New("coverage report has no total")
}

func offline(ctx context.Context, root string) error {
	for _, directory := range []string{root, petclinicRoot(root)} {
		if err := runExternal(
			ctx,
			directory,
			map[string]string{"GOPROXY": "off"},
			"go",
			"test",
			"-mod=vendor",
			"-count=1",
			"./...",
		); err != nil {
			return err
		}
	}
	return nil
}

func smoke(ctx context.Context, root string) error {
	commands := [][]string{
		{"go", "run", "./cmd/spice", "verify", "./..."},
		{
			"go", "run", "./cmd/spice", "verify", "--format=json",
			"./examples/commerce/...",
		},
		{"go", "run", "./cmd/spice", "version"},
		{"go", "run", "./cmd/spice", "modules", "--format=json", "./..."},
		{
			"go", "run", "./cmd/spice", "test",
			"--module", "github.com/StevenBuglione/spice/examples/commerce/orders",
			"--count=1",
			"./examples/commerce/...",
		},
		{
			"go", "run", "./cmd/spice", "generate", "--check", "--target", "Commerce",
			"./examples/commerce/...",
		},
		{
			"go", "run", "./cmd/spice", "run", "--target", "Commerce",
			"./examples/commerce/...", "--", "-check",
		},
	}
	for _, args := range commands {
		if err := runExternal(ctx, root, nil, args[0], args[1:]...); err != nil {
			return err
		}
	}
	if err := petclinicSmoke(ctx, root); err != nil {
		return err
	}
	return thirdPartyAnnotationSmoke(ctx, root)
}

func petclinicSmoke(ctx context.Context, root string) error {
	temp, err := os.MkdirTemp("", "spice-petclinic-*")
	if err != nil {
		return fmt.Errorf("create Petclinic smoke directory: %w", err)
	}
	defer removeTemporaryDirectory(temp)
	executable := filepath.Join(temp, "spice")
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	offline := map[string]string{
		"GOPROXY": "off",
		"SPICE_PETCLINIC_POSTGRES_URL": "postgres://petclinic:petclinic@" +
			"127.0.0.1:1/petclinic?sslmode=disable",
		"SPICE_PETCLINIC_POSTGRES_ALLOW_INSECURE": "true",
		"SPICE_PETCLINIC_MYSQL_URL": "mysql://petclinic:petclinic@" +
			"127.0.0.1:1/petclinic?tls=disable",
		"SPICE_PETCLINIC_MYSQL_ALLOW_INSECURE": "true",
	}
	if err := runExternal(
		ctx,
		root,
		offline,
		"go",
		"build",
		"-trimpath",
		"-o",
		executable,
		"./cmd/spice",
	); err != nil {
		return err
	}
	directory := petclinicRoot(root)
	memoryPatterns := []string{
		".",
		"./memory",
		"./model",
		"./owner",
		"./presentation",
		"./system",
		"./vet",
	}
	postgresPatterns := []string{
		"./cmd/postgres",
		"./owner",
		"./postgres",
		"./presentation",
		"./system",
		"./vet",
	}
	mysqlPatterns := []string{
		"./cmd/mysql",
		"./mysql",
		"./owner",
		"./presentation",
		"./system",
		"./vet",
	}
	commands := [][]string{
		append([]string{"verify"}, memoryPatterns...),
		append(
			[]string{"generate", "--check", "--target", "Petclinic"},
			memoryPatterns...,
		),
		append(
			append(
				[]string{"run", "--target", "Petclinic"},
				memoryPatterns...,
			),
			"--",
			"-check",
		),
		append([]string{"verify"}, postgresPatterns...),
		append(
			[]string{"generate", "--check", "--target", "Postgres"},
			postgresPatterns...,
		),
		append(
			append(
				[]string{"run", "--target", "Postgres"},
				postgresPatterns...,
			),
			"--",
			"-check",
		),
		append([]string{"verify"}, mysqlPatterns...),
		append(
			[]string{"generate", "--check", "--target", "Mysql"},
			mysqlPatterns...,
		),
		append(
			append(
				[]string{"run", "--target", "Mysql"},
				mysqlPatterns...,
			),
			"--",
			"-check",
		),
	}
	for _, arguments := range commands {
		if err := runExternal(
			ctx,
			directory,
			offline,
			executable,
			arguments...,
		); err != nil {
			return err
		}
	}
	return nil
}

func thirdPartyAnnotationSmoke(
	ctx context.Context,
	root string,
) error {
	pluginRoot := filepath.Join(root, "testdata", "annotationfixture")
	applicationRoot := filepath.Join(root, "testdata", "annotationapp")
	offline := map[string]string{"GOPROXY": "off"}
	for _, moduleRoot := range []string{pluginRoot, applicationRoot} {
		for _, arguments := range [][]string{
			{"mod", "tidy", "-diff"},
			{"test", "./..."},
			{"vet", "./..."},
		} {
			if err := runExternal(
				ctx,
				moduleRoot,
				offline,
				"go",
				arguments...,
			); err != nil {
				return err
			}
		}
	}
	temp, err := os.MkdirTemp("", "spice-annotation-fixture-*")
	if err != nil {
		return fmt.Errorf(
			"create annotation fixture directory: %w",
			err,
		)
	}
	defer removeTemporaryDirectory(temp)
	executable := filepath.Join(temp, "spice")
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	if err := runExternal(
		ctx,
		root,
		offline,
		"go",
		"build",
		"-trimpath",
		"-o",
		executable,
		"./cmd/spice",
	); err != nil {
		return err
	}
	for _, arguments := range [][]string{
		{"annotations", "list", "./..."},
		{"annotations", "doctor", "./..."},
		{"verify", "--format=json", "./..."},
		{"generate", "--check", "./..."},
		{"run", "./...", "--", "-check"},
	} {
		if err := runExternal(
			ctx,
			applicationRoot,
			offline,
			executable,
			arguments...,
		); err != nil {
			return err
		}
	}
	return nil
}

func toolPath(ctx context.Context, root, name string) (string, error) {
	stdout, err := captureExternal(ctx, root, "go", "tool", "-C", "tools", "-n", name)
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(stdout)
	if path == "" {
		return "", fmt.Errorf("resolve tool %q: empty path", name)
	}
	return path, nil
}

func command(
	ctx context.Context,
	directory string,
	environment map[string]string,
	executable string,
	args ...string,
) error {
	if err := validateExecutable(executable); err != nil {
		return err
	}
	// #nosec G204 -- validateExecutable restricts execution to approved repository tools and toolchains.
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = directory
	cmd.Env = mergedEnvironment(environment)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", executable, strings.Join(args, " "), err)
	}
	return nil
}

func capture(
	ctx context.Context,
	directory string,
	executable string,
	args ...string,
) (string, error) {
	if err := validateExecutable(executable); err != nil {
		return "", err
	}
	// #nosec G204 -- validateExecutable restricts execution to approved repository tools and toolchains.
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = directory
	cmd.Env = os.Environ()
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf(
			"%s %s: %w\n%s",
			executable,
			strings.Join(args, " "),
			err,
			strings.TrimSpace(string(output)),
		)
	}
	return string(output), nil
}

func validateExecutable(executable string) error {
	name := strings.TrimSuffix(strings.ToLower(filepath.Base(executable)), ".exe")
	switch name {
	case "cargo", "git", "go", "gofumpt", "goimports", "golangci-lint", "gosec", "govulncheck", "gradlew", "gradlew.bat", "nilaway", "rustc", "spice", "xvfb-run":
		return nil
	default:
		return fmt.Errorf("executable %q is not an approved quality tool", executable)
	}
}

func removeTemporaryDirectory(path string) {
	if err := os.RemoveAll(path); err != nil {
		output.Printf("warning: remove temporary directory %q: %v", path, err)
	}
}

func mergedEnvironment(overrides map[string]string) []string {
	if len(overrides) == 0 {
		return os.Environ()
	}
	environment := make(map[string]string)
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		environment[environmentKey(key)] = value
	}
	for key, value := range overrides {
		environment[environmentKey(key)] = key + "=" + value
	}
	values := make([]string, 0, len(environment))
	for _, value := range environment {
		values = append(values, value)
	}
	slices.Sort(values)
	return values
}

func environmentKey(key string) string {
	if runtime.GOOS == "windows" {
		return strings.ToUpper(key)
	}
	return key
}
