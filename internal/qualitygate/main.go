// Command qualitygate runs Spice's repository-owned, cross-platform verification.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
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

	"github.com/spice-framework/spice/internal/bootstrapcheck"
	"github.com/spice-framework/spice/internal/qualitygate/fastgate"
)

const (
	requiredGoVersion           = "go1.26.5"
	minimumCoverage             = 85.0
	maximumGeneratedTargetLines = fastgate.MaximumGeneratedTargetLines
	fuzzSmokeExecutions         = "100x"
	modulePath                  = "github.com/spice-framework/spice"
	legacyModulePath            = "github.com/" + "StevenBuglione/spice"
)

var output = log.New(os.Stdout, "", 0)

var (
	runExternal       = command
	captureExternal   = capture
	runBootstrapCheck = bootstrapcheck.Run
)

func main() {
	os.Exit(execute()) // Entrypoint exception: propagate a failed verification to make and CI.
}

func execute() int {
	mode := flag.String("mode", "verify", "verification mode: benchmark, bootstrap, check, coverage, dogfood, fmt, fuzz, lint, security, smoke, test, vet, offline, verify, or verify-release")
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
		"dogfood":        func() error { return dogfood(ctx, root) },
		"fmt":            func() error { return format(ctx, root, true) },
		"fuzz":           func() error { return fuzz(ctx, root) },
		"lint":           func() error { return lint(ctx, root) },
		"security":       func() error { return security(ctx, root) },
		"smoke":          func() error { return smoke(ctx, root) },
		"test":           func() error { return test(ctx, root) },
		"vet":            func() error { return vet(ctx, root) },
		"offline":        func() error { return offline(ctx, root) },
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
		{"canonical repository namespace", func() error {
			return checkCanonicalNamespace(root)
		}},
		{"API maturity coverage", func() error {
			return checkAPIMaturity(ctx, root)
		}},
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

func vet(ctx context.Context, root string) error {
	return runExternal(ctx, root, nil, "go", "vet", "./...")
}

func compileTests(ctx context.Context, root string) error {
	return runExternal(ctx, root, nil, "go", "test", "-run=^$", "./...")
}

func verify(ctx context.Context, root string, release bool) error {
	if err := runSequential([]verificationStep{
		{"canonical repository namespace", func() error {
			return checkCanonicalNamespace(root)
		}},
		{"API maturity coverage", func() error {
			return checkAPIMaturity(ctx, root)
		}},
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
	}
	if err := runParallel(parallelSteps, 4); err != nil {
		return err
	}
	if release {
		if err := runStep(verificationStep{
			name: "benchmark budgets",
			run:  func() error { return benchmark(ctx, root) },
		}); err != nil {
			return err
		}
	}
	// One shuffled, race-enabled pass emits the repository coverage profile, so
	// the complete product graph is compiled and executed only once here. The
	// remaining broad stages stay sequential; executable smoke owns its own
	// bounded concurrency because its scenarios use independent workspaces.
	if err := runSequential([]verificationStep{
		{"tests and coverage", func() error {
			return testAndCoverage(ctx, root)
		}},
		{"fuzz smoke tests", func() error { return fuzz(ctx, root) }},
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

func checkCanonicalNamespace(root string) (resultErr error) {
	repository, err := os.OpenRoot(root)
	if err != nil {
		return fmt.Errorf("open canonical namespace root: %w", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, repository.Close())
	}()

	var matches []string
	err = fs.WalkDir(repository.FS(), ".", func(
		filename string,
		entry fs.DirEntry,
		walkErr error,
	) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if filename == "." {
				return nil
			}
			switch entry.Name() {
			case ".git", ".gradle", ".idea", ".intellijPlatform", ".tmp", "bin", "build", "node_modules", "out", "target":
				return filepath.SkipDir
			default:
				return nil
			}
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		content, readErr := repository.ReadFile(filepath.FromSlash(filename))
		if readErr != nil {
			return fmt.Errorf("read repository source %q: %w", filename, readErr)
		}
		if !bytes.Contains(content, []byte(legacyModulePath)) {
			return nil
		}
		matches = append(matches, filename)
		return nil
	})
	if err != nil {
		return fmt.Errorf("scan canonical repository namespace: %w", err)
	}
	if len(matches) != 0 {
		slices.Sort(matches)
		return fmt.Errorf(
			"legacy module namespace %q remains in %s",
			legacyModulePath,
			strings.Join(matches, ", "),
		)
	}
	return nil
}

type apiMaturityPolicy struct {
	Schema          string            `json:"schema"`
	Module          string            `json:"module"`
	Classifications []apiMaturityRule `json:"classifications"`
}

type apiMaturityRule struct {
	Prefix   string `json:"prefix"`
	Maturity string `json:"maturity"`
	Reason   string `json:"reason"`
}

func checkAPIMaturity(ctx context.Context, root string) error {
	content, err := readRootFile(root, "docs/api-compatibility.json")
	if err != nil {
		return fmt.Errorf("read API maturity policy: %w", err)
	}
	packages, err := captureExternal(
		ctx,
		root,
		"go",
		"list",
		"-mod=vendor",
		"-f",
		"{{.ImportPath}}",
		"./...",
	)
	if err != nil {
		return fmt.Errorf("list API packages: %w", err)
	}
	return validateAPIMaturity(content, packages)
}

func validateAPIMaturity(content []byte, packageOutput string) error {
	policy, err := decodeAPIMaturityPolicy(content)
	if err != nil {
		return err
	}
	return validateAPIMaturityPackages(policy.Classifications, packageOutput)
}

func decodeAPIMaturityPolicy(content []byte) (apiMaturityPolicy, error) {
	var policy apiMaturityPolicy
	if err := json.Unmarshal(content, &policy); err != nil {
		return apiMaturityPolicy{}, fmt.Errorf("decode API maturity policy: %w", err)
	}
	if policy.Schema != "spice.api-maturity/v1" {
		return apiMaturityPolicy{}, fmt.Errorf(
			"API maturity policy schema = %q, want spice.api-maturity/v1",
			policy.Schema,
		)
	}
	if policy.Module != modulePath {
		return apiMaturityPolicy{}, fmt.Errorf(
			"API maturity policy module = %q, want %q",
			policy.Module,
			modulePath,
		)
	}
	if len(policy.Classifications) == 0 {
		return apiMaturityPolicy{}, errors.New("API maturity policy has no classifications")
	}
	seen := make(map[string]struct{}, len(policy.Classifications))
	for _, rule := range policy.Classifications {
		if err := validateAPIMaturityRule(rule, seen); err != nil {
			return apiMaturityPolicy{}, err
		}
	}
	return policy, nil
}

func validateAPIMaturityRule(
	rule apiMaturityRule,
	seen map[string]struct{},
) error {
	if rule.Prefix == "" ||
		rule.Prefix != strings.TrimSpace(rule.Prefix) ||
		strings.HasPrefix(rule.Prefix, "/") ||
		strings.HasSuffix(rule.Prefix, "/") ||
		strings.Contains(rule.Prefix, "\\") {
		return fmt.Errorf("API maturity prefix %q is invalid", rule.Prefix)
	}
	if _, duplicate := seen[rule.Prefix]; duplicate {
		return fmt.Errorf("API maturity prefix %q is duplicated", rule.Prefix)
	}
	seen[rule.Prefix] = struct{}{}
	if !slices.Contains(
		[]string{"preview-stable", "experimental", "internal"},
		rule.Maturity,
	) {
		return fmt.Errorf(
			"API maturity prefix %q has invalid maturity %q",
			rule.Prefix,
			rule.Maturity,
		)
	}
	if strings.TrimSpace(rule.Reason) == "" {
		return fmt.Errorf("API maturity prefix %q requires a reason", rule.Prefix)
	}
	return nil
}

func validateAPIMaturityPackages(
	rules []apiMaturityRule,
	packageOutput string,
) error {
	packages := strings.Fields(packageOutput)
	if len(packages) == 0 {
		return errors.New("go package listing is empty")
	}
	matched := make(map[string]bool, len(rules))
	for _, packagePath := range packages {
		if packagePath != modulePath && !strings.HasPrefix(packagePath, modulePath+"/") {
			continue
		}
		relative := strings.TrimPrefix(packagePath, modulePath+"/")
		if !matchAPIMaturityRules(relative, rules, matched) {
			return fmt.Errorf("go package %s has no API maturity classification", packagePath)
		}
	}
	for _, rule := range rules {
		if !matched[rule.Prefix] {
			return fmt.Errorf("API maturity prefix %q matches no Go package", rule.Prefix)
		}
	}
	return nil
}

func matchAPIMaturityRules(
	relative string,
	rules []apiMaturityRule,
	matched map[string]bool,
) bool {
	found := false
	for _, rule := range rules {
		if relative != rule.Prefix && !strings.HasPrefix(relative, rule.Prefix+"/") {
			continue
		}
		matched[rule.Prefix] = true
		found = true
	}
	return found
}

func checkGeneratedTargetBoundaries(root string) error {
	return fastgate.CheckGeneratedTargetBoundaries(root)
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
	directories := []string{filepath.Join(root, "tools"), root}
	for _, directory := range directories {
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
	return checkModuleVendor(ctx, root)
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
	if commandErr := runExternal(
		ctx,
		root,
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
	if commandErr := runExternal(
		ctx,
		root,
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
	return runExternal(ctx, root, nil, govulncheck, "./...")
}

func test(ctx context.Context, root string) error {
	return runTestCommands(ctx, root, nil)
}

func testAndCoverage(
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
	profile := filepath.Join(temp, profileName)
	if err := runExternal(
		ctx,
		root,
		nil,
		"go",
		"test",
		"-race",
		"-shuffle=on",
		"-count=1",
		"-covermode=atomic",
		"-coverprofile="+profile,
		"./...",
	); err != nil {
		return err
	}
	return enforceCoverageProfile(
		ctx,
		root,
		temp,
		coverageRoot,
		profileName,
	)
}

func runTestCommands(
	ctx context.Context,
	root string,
	normalArguments []string,
) error {
	arguments := []string{"test", "-shuffle=on", "-count=1"}
	arguments = append(arguments, normalArguments...)
	arguments = append(arguments, "./...")
	if err := runExternal(ctx, root, nil, "go", arguments...); err != nil {
		return err
	}
	return runExternal(
		ctx,
		root,
		nil,
		"go",
		"test",
		"-race",
		"-shuffle=on",
		"-count=1",
		"./...",
	)
}

func fuzz(ctx context.Context, root string) error {
	targets := []struct {
		pkg    string
		target string
	}{
		{"./expression", "FuzzCompile"},
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
		{"./annotation/sdk/starter", "FuzzParseManifest"},
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
			"-fuzztime="+fuzzSmokeExecutions,
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
	profile := filepath.Join(temp, profileName)
	if commandErr := runExternal(ctx, root, nil, "go", "test", "-covermode=atomic", "-coverprofile="+profile, "./..."); commandErr != nil {
		return commandErr
	}
	if err := enforceCoverageProfile(
		ctx,
		root,
		temp,
		coverageRoot,
		profileName,
	); err != nil {
		return err
	}
	return nil
}

func enforceCoverageProfile(
	ctx context.Context,
	root string,
	coverageDirectory string,
	coverageRoot *os.Root,
	profileName string,
) error {
	const productProfileName = "coverage-product.out"
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
		"-func="+filepath.Join(coverageDirectory, productProfileName),
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
	return nil
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
	return runParallel([]verificationStep{
		{name: "clean-room application", run: func() error {
			return scaffoldSmoke(ctx, root)
		}},
		{name: "dogfood runtime", run: func() error {
			return dogfoodRuntime(ctx, root)
		}},
		{name: "module report", run: func() error {
			return runExternal(
				ctx,
				root,
				nil,
				"go",
				"run",
				"./cmd/spice",
				"modules",
				"--format=json",
				"./...",
			)
		}},
		{name: "third-party annotations", run: func() error {
			return thirdPartyAnnotationSmoke(ctx, root)
		}},
	}, 2)
}

func scaffoldSmoke(ctx context.Context, root string) error {
	temp, tempErr := os.MkdirTemp("", "spice-clean-room-*")
	if tempErr != nil {
		return fmt.Errorf("create clean-room smoke directory: %w", tempErr)
	}
	defer removeTemporaryDirectory(temp)
	executable := filepath.Join(temp, "spice")
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	offline := map[string]string{"GOPROXY": "off"}
	if buildErr := runExternal(
		ctx,
		root,
		offline,
		"go",
		"build",
		"-mod=vendor",
		"-trimpath",
		"-o",
		executable,
		"./cmd/spice",
	); buildErr != nil {
		return buildErr
	}
	applicationRoot := filepath.Join(temp, "application")
	if createErr := runExternal(
		ctx,
		root,
		offline,
		executable,
		"new",
		"--module",
		"example.com/spice-clean-room",
		"--directory",
		applicationRoot,
		"--spice-version",
		"v0.0.0",
		"--replace",
		root,
	); createErr != nil {
		return createErr
	}
	dependencySource := []byte("package main\n\n" +
		"import \"golang.org/x/sync/errgroup\"\n\n" +
		"var _ errgroup.Group\n")
	if writeErr := os.WriteFile(
		filepath.Join(applicationRoot, "dependency.go"),
		dependencySource,
		0o600,
	); writeErr != nil {
		return fmt.Errorf("write clean-room dependency source: %w", writeErr)
	}
	if err := cleanRoomDependencySmoke(
		ctx,
		applicationRoot,
		offline,
		executable,
	); err != nil {
		return err
	}
	return runCleanRoomCommands(ctx, applicationRoot, offline, executable)
}

func cleanRoomDependencySmoke(
	ctx context.Context,
	applicationRoot string,
	offline map[string]string,
	executable string,
) error {
	application, openErr := os.OpenRoot(applicationRoot)
	if openErr != nil {
		return fmt.Errorf("open clean-room application: %w", openErr)
	}
	smokeErr := cleanRoomDependencySmokeInRoot(
		ctx,
		applicationRoot,
		application,
		offline,
		executable,
	)
	return errors.Join(smokeErr, application.Close())
}

func cleanRoomDependencySmokeInRoot(
	ctx context.Context,
	applicationRoot string,
	application *os.Root,
	offline map[string]string,
	executable string,
) error {
	before, readErr := application.ReadFile("go.mod")
	if readErr != nil {
		return fmt.Errorf("read clean-room go.mod before preview: %w", readErr)
	}
	selector := "golang.org/x/sync/errgroup@v0.22.0"
	if previewErr := runExternal(
		ctx,
		applicationRoot,
		offline,
		executable,
		"add",
		selector,
	); previewErr != nil {
		return previewErr
	}
	afterPreview, readPreviewErr := application.ReadFile("go.mod")
	if readPreviewErr != nil {
		return fmt.Errorf("read clean-room go.mod after preview: %w", readPreviewErr)
	}
	if !bytes.Equal(before, afterPreview) {
		return errors.New("spice add preview changed the clean-room go.mod")
	}
	if applyErr := runExternal(
		ctx,
		applicationRoot,
		offline,
		executable,
		"add",
		"--apply",
		selector,
	); applyErr != nil {
		return applyErr
	}
	afterApply, readApplyErr := application.ReadFile("go.mod")
	if readApplyErr != nil {
		return fmt.Errorf("read clean-room go.mod after apply: %w", readApplyErr)
	}
	if bytes.Equal(before, afterApply) ||
		!bytes.Contains(afterApply, []byte("golang.org/x/sync v0.22.0")) {
		return errors.New("spice add did not apply the previewed clean-room dependency")
	}
	return nil
}

func runCleanRoomCommands(
	ctx context.Context,
	applicationRoot string,
	offline map[string]string,
	executable string,
) error {
	commands := []struct {
		executable string
		arguments  []string
	}{
		{executable: "go", arguments: []string{"mod", "download"}},
		{executable: executable, arguments: []string{"generate", "--target", "Spice-clean-room", "."}},
		{executable: executable, arguments: []string{"verify", "."}},
		{executable: "go", arguments: []string{"mod", "tidy"}},
		{executable: "go", arguments: []string{"mod", "vendor"}},
		{executable: executable, arguments: []string{"generate", "--check", "--target", "Spice-clean-room", "."}},
		{executable: "go", arguments: []string{"test", "-mod=vendor", "./..."}},
		{executable: executable, arguments: []string{"build", "--target", "Spice-clean-room", "."}},
		{executable: executable, arguments: []string{"run", "--target", "Spice-clean-room", ".", "--", "-check"}},
	}
	for _, command := range commands {
		if runErr := runExternal(
			ctx,
			applicationRoot,
			offline,
			command.executable,
			command.arguments...,
		); runErr != nil {
			return runErr
		}
	}
	return nil
}

func dogfood(ctx context.Context, root string) error {
	return runSequential([]verificationStep{
		{
			name: "dogfood generated boundaries",
			run:  func() error { return checkGeneratedTargetBoundaries(root) },
		},
		{
			name: "dogfood formatting",
			run:  func() error { return format(ctx, root, false) },
		},
		{
			name: "dogfood focused tests",
			run: func() error {
				return runExternal(
					ctx,
					root,
					nil,
					"go",
					"test",
					"-count=1",
					"-run=^(TestCatalogAllowsSelectableEntrypointDuplicateOutputs|TestCatalogRejectsNonSelectableGeneratedDuplicateOutputs|TestCommandRunsVersionThroughProductionBoundary|TestCommandHandler.*|TestRuntime.*|TestDefaultCommand.*|TestDescriptorDocumentsFallbackProvenance|TestApplication.*|TestEnginePreservesLastKnownGoodAndRecovers|TestServerDeveloperWorkflowUsesVersionedCompilerResults)$",
					"./compiler/provider",
					"./internal/cli",
					"./internal/autoconfigure",
					"./internal/devloop",
					"./internal/lsp",
					"./internal/spicegen/spice",
					"./cmd/spice",
				)
			},
		},
		{
			name: "dogfood runtime",
			run:  func() error { return dogfoodRuntime(ctx, root) },
		},
	})
}

func dogfoodRuntime(ctx context.Context, root string) error {
	temp, err := os.MkdirTemp("", "spice-dogfood-*")
	if err != nil {
		return fmt.Errorf("create dogfood directory: %w", err)
	}
	defer removeTemporaryDirectory(temp)

	executable := filepath.Join(temp, "spice")
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	offline := map[string]string{"GOPROXY": "off"}
	modulePatterns := []string{
		"./compiler/...",
		"./internal/devloop",
		"./internal/genfs",
		"./internal/lsp",
		"./internal/cli",
		"./internal/scaffold",
		"./internal/spiceapp",
	}
	applicationPatterns := append(
		append([]string(nil), modulePatterns...),
		"./internal/autoconfigure",
	)

	if err := runExternal(
		ctx,
		root,
		offline,
		"go",
		"build",
		"-mod=vendor",
		"-trimpath",
		"-o",
		executable,
		"./cmd/spice-bootstrap",
	); err != nil {
		return err
	}
	if err := runExternal(
		ctx,
		root,
		offline,
		executable,
		append(
			[]string{"generate", "--check", "--target", "Spice"},
			applicationPatterns...,
		)...,
	); err != nil {
		return err
	}

	if err := runExternal(
		ctx,
		root,
		offline,
		"go",
		"build",
		"-mod=vendor",
		"-trimpath",
		"-o",
		executable,
		"./cmd/spice",
	); err != nil {
		return err
	}
	commands := [][]string{
		{"version"},
		append(
			[]string{"generate", "--check", "--target", "Spice"},
			applicationPatterns...,
		),
		append(
			[]string{"beans", "--explain", "--format=json"},
			applicationPatterns...,
		),
		append(
			[]string{"modules", "--format=json"},
			modulePatterns...,
		),
		{
			"generated",
			"--source",
			"internal/autoconfigure/version_handler.go",
			"--target",
			"spice",
			"--format",
			"json",
		},
		append(
			[]string{
				"test",
				"--module",
				modulePath + "/internal/cli",
				"--count=1",
				"--run=^TestCommandRunsVersionThroughProductionBoundary$",
			},
			modulePatterns...,
		),
	}
	for _, arguments := range commands {
		if err := runExternal(
			ctx,
			root,
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
	// #nosec G204,G702 -- validateExecutable restricts the executable and CommandContext receives discrete arguments without a shell.
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
	// #nosec G204,G702 -- validateExecutable restricts the executable and CommandContext receives discrete arguments without a shell.
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = directory
	cmd.Env = os.Environ()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		detail := strings.TrimSpace(strings.Join(
			[]string{stdout.String(), stderr.String()},
			"\n",
		))
		return "", fmt.Errorf(
			"%s %s: %w\n%s",
			executable,
			strings.Join(args, " "),
			err,
			detail,
		)
	}
	return stdout.String(), nil
}

func validateExecutable(executable string) error {
	name := strings.TrimSuffix(strings.ToLower(filepath.Base(executable)), ".exe")
	switch name {
	case "git", "go", "gofumpt", "goimports", "golangci-lint", "gosec", "govulncheck", "gradlew", "gradlew.bat", "nilaway", "spice", "xvfb-run":
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
