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
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	requiredGoVersion   = "go1.26.5"
	requiredRustVersion = "1.93.0"
	minimumCoverage     = 85.0
	modulePath          = "github.com/StevenBuglione/spice"
)

var output = log.New(os.Stdout, "", 0)

var (
	runExternal     = command
	captureExternal = capture
)

func main() {
	os.Exit(execute()) // Entrypoint exception: propagate a failed verification to make and CI.
}

func execute() int {
	mode := flag.String("mode", "verify", "verification mode: fmt, fuzz, lint, security, smoke, test, vet, offline, zed, or verify")
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

	switch mode {
	case "fmt":
		return format(ctx, root, true)
	case "fuzz":
		return fuzz(ctx, root)
	case "lint":
		return lint(ctx, root)
	case "security":
		return security(ctx, root)
	case "smoke":
		return smoke(ctx, root)
	case "test":
		return test(ctx, root)
	case "vet":
		return runExternal(ctx, root, nil, "go", "vet", "./...")
	case "offline":
		return offline(ctx, root)
	case "zed":
		return zed(ctx, root)
	case "verify":
		return verify(ctx, root)
	default:
		return fmt.Errorf("unknown mode %q", mode)
	}
}

func verify(ctx context.Context, root string) error {
	steps := []struct {
		name string
		run  func() error
	}{
		{"formatting", func() error { return format(ctx, root, false) }},
		{"module tidiness", func() error { return checkModuleTidy(ctx, root) }},
		{"vendor consistency", func() error { return checkVendor(ctx, root) }},
		{"go vet", func() error { return runExternal(ctx, root, nil, "go", "vet", "./...") }},
		{"lint and nil safety", func() error { return lint(ctx, root) }},
		{"security", func() error { return security(ctx, root) }},
		{"Zed extension", func() error { return zed(ctx, root) }},
		{"tests", func() error { return test(ctx, root) }},
		{"fuzz smoke tests", func() error { return fuzz(ctx, root) }},
		{"coverage", func() error { return coverage(ctx, root) }},
		{"offline vendor tests", func() error { return offline(ctx, root) }},
		{"executable smoke tests", func() error { return smoke(ctx, root) }},
	}
	for _, step := range steps {
		output.Printf("==> %s", step.name)
		if err := step.run(); err != nil {
			return fmt.Errorf("%s: %w", step.name, err)
		}
	}
	output.Println("==> all verification passed")
	return nil
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
	for _, directory := range []string{root, filepath.Join(root, "tools")} {
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
	temp, err := os.MkdirTemp("", "spice-vendor-*")
	if err != nil {
		return fmt.Errorf("create temporary vendor directory: %w", err)
	}
	defer removeTemporaryDirectory(temp)

	generated := filepath.Join(temp, "vendor")
	if commandErr := runExternal(ctx, root, nil, "go", "mod", "vendor", "-o", generated); commandErr != nil {
		return commandErr
	}
	want, err := treeDigest(generated)
	if err != nil {
		return err
	}
	got, err := treeDigest(filepath.Join(root, "vendor"))
	if err != nil {
		return err
	}
	if !equalDigests(want, got) {
		return errors.New("vendor directory is stale; run go mod vendor")
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
	nilaway, err := toolPath(ctx, root, "nilaway")
	if err != nil {
		return err
	}
	return runExternal(
		ctx,
		root,
		nil,
		nilaway,
		"-include-pkgs="+modulePath,
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
	govulncheck, err := toolPath(ctx, root, "govulncheck")
	if err != nil {
		return err
	}
	return runExternal(ctx, root, nil, govulncheck, "./...")
}

func test(ctx context.Context, root string) error {
	if err := runExternal(ctx, root, nil, "go", "test", "-shuffle=on", "-count=1", "./..."); err != nil {
		return err
	}
	return runExternal(ctx, root, nil, "go", "test", "-race", "-shuffle=on", "-count=1", "./...")
}

func fuzz(ctx context.Context, root string) error {
	targets := []struct {
		pkg    string
		target string
	}{
		{"./compiler/diagnostic", "FuzzDiagnosticReportJSON"},
		{"./compiler/load", "FuzzGeneratedMainBridgeError"},
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

func coverage(ctx context.Context, root string) error {
	temp, err := os.MkdirTemp("", "spice-coverage-*")
	if err != nil {
		return fmt.Errorf("create coverage directory: %w", err)
	}
	defer removeTemporaryDirectory(temp)

	profile := filepath.Join(temp, "coverage.out")
	if commandErr := runExternal(ctx, root, nil, "go", "test", "-covermode=atomic", "-coverprofile="+profile, "./..."); commandErr != nil {
		return commandErr
	}
	stdout, err := captureExternal(ctx, root, "go", "tool", "cover", "-func="+profile)
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
	return nil
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
	return runExternal(
		ctx,
		root,
		map[string]string{"GOPROXY": "off"},
		"go",
		"test",
		"-mod=vendor",
		"-count=1",
		"./...",
	)
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
	case "cargo", "go", "gofumpt", "goimports", "golangci-lint", "gosec", "govulncheck", "nilaway", "rustc", "spice":
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
