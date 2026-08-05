// Package boundarygate owns the focused, cross-platform verification contract
// for the standalone Spice toolchain repository.
package boundarygate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/spice-framework/toolchain/internal/identity"
)

const requiredGoVersion = "go1.26.5"

const maxCommandFailureOutput = 32 * 1024

var checkedPackages = []string{
	"./cmd/spice",
	"./cmd/spice-annotation-core",
	"./cmd/spice-bootstrap",
	"./compiler/...",
	"./internal/annotationcore",
	"./internal/boundarygate/...",
	"./internal/cli",
	"./internal/identity",
	"./internal/lsp",
	"./internal/scaffold",
	"./internal/testsupport",
}

// Run executes one repository verification mode.
func Run(ctx context.Context, root, mode string, output io.Writer) error {
	if ctx == nil {
		return errors.New("boundary gate context is nil")
	}
	if output == nil {
		output = io.Discard
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return fmt.Errorf("resolve repository root: %w", err)
	}
	gate := verifier{root: filepath.Clean(absoluteRoot), output: output}
	switch mode {
	case "fast":
		return gate.fast(ctx)
	case "check":
		return gate.check(ctx)
	case "verify":
		return gate.verify(ctx)
	default:
		return fmt.Errorf("unknown boundary gate mode %q", mode)
	}
}

type verifier struct {
	root           string
	output         io.Writer
	execute        commandExecutor
	executeStreams commandStreamExecutor
}

type commandExecutor func(
	context.Context,
	string,
	map[string]string,
	string,
	...string,
) ([]byte, error)

type commandStreamExecutor func(
	context.Context,
	string,
	map[string]string,
	string,
	...string,
) ([]byte, []byte, error)

func (gate verifier) fast(ctx context.Context) error {
	if err := gate.goVersion(ctx); err != nil {
		return err
	}
	if err := gate.identityBoundary(); err != nil {
		return err
	}
	if err := gate.run(ctx, gate.root, nil, "go", append(
		[]string{"test", "-count=1"},
		"./cmd/spice",
		"./internal/annotationcore",
		"./internal/identity",
		"./internal/scaffold",
	)...); err != nil {
		return err
	}
	return gate.buildTools(ctx)
}

func (gate verifier) check(ctx context.Context) error {
	if err := gate.fast(ctx); err != nil {
		return err
	}
	return gate.run(
		ctx,
		gate.root,
		nil,
		"go",
		append([]string{"test", "-count=1"}, checkedPackages...)...,
	)
}

func (gate verifier) verify(ctx context.Context) error {
	steps := []struct {
		name string
		run  func(context.Context) error
	}{
		{name: "Go version", run: gate.goVersion},
		{name: "source identity", run: func(context.Context) error {
			return gate.identityBoundary()
		}},
		{name: "Go formatting", run: gate.formatting},
		{name: "module tidiness", run: gate.moduleTidiness},
		{name: "module checksums", run: func(ctx context.Context) error {
			return gate.run(ctx, gate.root, nil, "go", "mod", "verify")
		}},
		{name: "vendor reproducibility", run: gate.vendorReproducibility},
		{name: "published tool builds", run: gate.buildTools},
		{name: "bootstrap dependency boundary", run: gate.bootstrapDependencies},
		{name: "vet", run: gate.vet},
		{name: "lint and nil safety", run: gate.lint},
		{name: "security", run: gate.security},
		{name: "race and coverage tests", run: gate.coverage},
		{name: "fuzz smoke", run: gate.fuzz},
		{name: "vendor-offline tests", run: gate.offlineTests},
		{name: "third-party generation", run: gate.thirdPartyGeneration},
	}
	for _, step := range steps {
		if _, err := fmt.Fprintf(gate.output, "==> %s\n", step.name); err != nil {
			return fmt.Errorf("write verification progress: %w", err)
		}
		if err := step.run(ctx); err != nil {
			return fmt.Errorf("%s: %w", step.name, err)
		}
	}
	_, err := fmt.Fprintln(gate.output, "Standalone Spice toolchain verification passed.")
	return err
}

func (gate verifier) goVersion(ctx context.Context) error {
	output, err := gate.capture(ctx, gate.root, nil, "go", "version")
	if err != nil {
		return err
	}
	fields := strings.Fields(string(output))
	if len(fields) < 3 || fields[2] != requiredGoVersion {
		return fmt.Errorf("go version is %q; require exactly %s", strings.TrimSpace(string(output)), requiredGoVersion)
	}
	return nil
}

func (gate verifier) formatting(ctx context.Context) error {
	files, err := gate.goFiles()
	if err != nil {
		return err
	}
	for _, name := range []string{"goimports", "gofumpt"} {
		formatter, err := gate.toolPath(ctx, name)
		if err != nil {
			return err
		}
		var unformatted []string
		for batch := range slices.Chunk(files, 100) {
			output, err := gate.capture(ctx, gate.root, nil, formatter, append([]string{"-l"}, batch...)...)
			if err != nil {
				return err
			}
			if text := strings.TrimSpace(string(output)); text != "" {
				unformatted = append(unformatted, strings.Fields(text)...)
			}
		}
		if len(unformatted) != 0 {
			sort.Strings(unformatted)
			return fmt.Errorf("%s required for %s", name, strings.Join(unformatted, ", "))
		}
	}
	return nil
}

func (gate verifier) goFiles() ([]string, error) {
	var files []string
	err := filepath.WalkDir(gate.root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "vendor" || entry.Name() == ".spice" || entry.Name() == "spicegen" {
				if path != gate.root {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if strings.HasSuffix(entry.Name(), ".go") {
			files = append(files, relativePath(gate.root, path))
		}
		return nil
	})
	sort.Strings(files)
	return files, err
}

func (gate verifier) moduleTidiness(ctx context.Context) error {
	for _, directory := range []string{".", "tools", "testdata/annotationfixture"} {
		if err := gate.run(
			ctx,
			filepath.Join(gate.root, filepath.FromSlash(directory)),
			nil,
			"go",
			"mod",
			"tidy",
			"-diff",
		); err != nil {
			return err
		}
	}
	return nil
}

func (gate verifier) vendorReproducibility(ctx context.Context) error {
	temporary, temporaryErr := os.MkdirTemp("", "spice-toolchain-vendor-")
	if temporaryErr != nil {
		return temporaryErr
	}
	defer gate.removeTemporary(temporary)
	generated := filepath.Join(temporary, "vendor")
	if err := gate.run(ctx, gate.root, nil, "go", "mod", "vendor", "-o", generated); err != nil {
		return err
	}
	want, err := snapshotTree(filepath.Join(gate.root, "vendor"))
	if err != nil {
		return fmt.Errorf("snapshot committed vendor: %w", err)
	}
	got, err := snapshotTree(generated)
	if err != nil {
		return fmt.Errorf("snapshot reproduced vendor: %w", err)
	}
	if !snapshotsEqual(want, got) {
		return errors.New("vendor differs from `go mod vendor`; run make vendor")
	}
	return nil
}

func (gate verifier) buildTools(ctx context.Context) error {
	temporary, temporaryErr := os.MkdirTemp("", "spice-toolchain-build-")
	if temporaryErr != nil {
		return temporaryErr
	}
	defer gate.removeTemporary(temporary)
	for name, pkg := range map[string]string{
		"spice":                 "./cmd/spice",
		"spice-annotation-core": "./cmd/spice-annotation-core",
		"spice-bootstrap":       "./cmd/spice-bootstrap",
	} {
		output := filepath.Join(temporary, name+executableSuffix())
		if err := gate.run(ctx, gate.root, nil, "go", "build", "-trimpath", "-o", output, pkg); err != nil {
			return err
		}
	}
	return nil
}

func (gate verifier) bootstrapDependencies(ctx context.Context) error {
	for _, pkg := range []string{"./cmd/spice", "./cmd/spice-bootstrap", "./cmd/spice-annotation-core"} {
		output, err := gate.capture(ctx, gate.root, nil, "go", "list", "-deps", pkg)
		if err != nil {
			return err
		}
		if bytes.Contains(output, []byte("/internal/spicegen/")) {
			return fmt.Errorf("%s depends on production generated code", pkg)
		}
	}
	return nil
}

func (gate verifier) vet(ctx context.Context) error {
	return gate.run(ctx, gate.root, nil, "go", "vet", "./...")
}

func (gate verifier) lint(ctx context.Context) error {
	golangci, resolveErr := gate.toolPath(ctx, "golangci-lint")
	if resolveErr != nil {
		return resolveErr
	}
	if err := gate.run(ctx, gate.root, nil, golangci, "run", "--timeout=10m"); err != nil {
		return err
	}
	nilaway, err := gate.toolPath(ctx, "nilaway")
	if err != nil {
		return err
	}
	return gate.run(ctx, gate.root, nil, nilaway, "-include-pkgs="+identity.ToolchainModule, "./...")
}

func (gate verifier) security(ctx context.Context) error {
	gosec, resolveErr := gate.toolPath(ctx, "gosec")
	if resolveErr != nil {
		return resolveErr
	}
	if err := gate.run(ctx, gate.root, nil, gosec, "-quiet", "-exclude-generated", "./..."); err != nil {
		return err
	}
	govulncheck, err := gate.toolPath(ctx, "govulncheck")
	if err != nil {
		return err
	}
	return gate.run(ctx, gate.root, nil, govulncheck, "./...")
}

func (gate verifier) coverage(ctx context.Context) error {
	temporary, temporaryErr := os.MkdirTemp("", "spice-toolchain-coverage-")
	if temporaryErr != nil {
		return temporaryErr
	}
	defer gate.removeTemporary(temporary)
	profile := filepath.Join(temporary, "coverage.out")
	if err := gate.run(
		ctx,
		gate.root,
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
	report, err := gate.capture(ctx, gate.root, nil, "go", "tool", "cover", "-func="+profile)
	if err != nil {
		return err
	}
	coverage, err := totalCoverage(string(report))
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(gate.output, "    coverage %.1f%% (minimum 85.0%%)\n", coverage); err != nil {
		return err
	}
	if coverage < 85.0 {
		return fmt.Errorf("repository coverage %.1f%% is below 85.0%%", coverage)
	}
	return nil
}

func totalCoverage(report string) (float64, error) {
	for line := range strings.SplitSeq(report, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == "total:" {
			value, err := strconv.ParseFloat(strings.TrimSuffix(fields[len(fields)-1], "%"), 64)
			if err != nil {
				return 0, fmt.Errorf("parse total coverage: %w", err)
			}
			return value, nil
		}
	}
	return 0, errors.New("coverage report has no total row")
}

func (gate verifier) fuzz(ctx context.Context) error {
	targets := []struct {
		pkg  string
		name string
	}{
		{pkg: "./compiler/diagnostic", name: "FuzzDiagnosticReportJSON"},
		{pkg: "./compiler/parser", name: "FuzzParseComment"},
		{pkg: "./compiler/service", name: "FuzzNormalizeOverlay"},
		{pkg: "./compiler/validate", name: "FuzzOccurrences"},
		{pkg: "./internal/lsp", name: "FuzzRPCReader"},
	}
	for _, target := range targets {
		if err := gate.run(ctx, gate.root, nil, "go", "test", target.pkg, "-run=^$", "-fuzz=^"+target.name+"$", "-fuzztime=100x"); err != nil {
			return err
		}
	}
	return nil
}

func (gate verifier) offlineTests(ctx context.Context) error {
	environment := map[string]string{
		"GOPROXY": "off",
		"GOSUMDB": "off",
		"GOWORK":  "off",
	}
	return gate.run(ctx, gate.root, environment, "go", "test", "-mod=vendor", "-count=1", "./...")
}

func (gate verifier) toolPath(ctx context.Context, name string) (string, error) {
	output, err := gate.capture(ctx, gate.root, nil, "go", "tool", "-C", "tools", "-n", name)
	if err != nil {
		return "", err
	}
	var paths []string
	for _, line := range strings.Split(strings.ReplaceAll(string(output), "\r\n", "\n"), "\n") {
		if path := strings.TrimSpace(line); path != "" {
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		return "", fmt.Errorf("resolve pinned tool %q: empty path", name)
	}
	if len(paths) != 1 {
		return "", fmt.Errorf("resolve pinned tool %q: expected one path, got %d", name, len(paths))
	}
	return paths[0], nil
}

func (gate verifier) identityBoundary() error {
	forbidden := []string{
		identity.CoreModule + "/compiler",
		identity.CoreModule + "/internal",
		identity.CoreModule + "/cmd",
	}
	allowed := map[string]struct{}{
		"compiler/service/third_party_integration_test.go": {},
		"internal/boundarygate/gate_test.go":               {},
		"internal/identity/identity.go":                    {},
		"internal/identity/identity_test.go":               {},
	}
	var violations []string
	root, err := os.OpenRoot(gate.root)
	if err != nil {
		return fmt.Errorf("open identity boundary root: %w", err)
	}
	walkErr := fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "vendor" || entry.Name() == ".spice" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".go") && !strings.HasSuffix(entry.Name(), ".mod") {
			return nil
		}
		relative := filepath.ToSlash(path)
		if _, ok := allowed[relative]; ok {
			return nil
		}
		content, err := root.ReadFile(path)
		if err != nil {
			return err
		}
		for _, value := range forbidden {
			if bytes.Contains(content, []byte(value)) {
				violations = append(violations, relative+": "+value)
			}
		}
		return nil
	})
	if err := errors.Join(walkErr, root.Close()); err != nil {
		return err
	}
	if len(violations) != 0 {
		sort.Strings(violations)
		return fmt.Errorf("stale monorepository identities: %s", strings.Join(violations, ", "))
	}
	return nil
}

func (gate verifier) thirdPartyGeneration(ctx context.Context) (resultErr error) {
	app := filepath.Join(gate.root, "testdata", "annotationapp")
	owned := []string{
		filepath.Join(app, ".spice"),
		filepath.Join(app, "bin"),
		filepath.Join(app, "internal", "spicegen"),
	}
	cleanup := func() error {
		var errs []error
		for _, path := range owned {
			if err := os.RemoveAll(path); err != nil {
				errs = append(errs, err)
			}
		}
		return errors.Join(errs...)
	}
	if cleanupErr := cleanup(); cleanupErr != nil {
		return fmt.Errorf("clear fixture generated output: %w", cleanupErr)
	}
	defer func() {
		resultErr = errors.Join(resultErr, cleanup())
	}()
	generate := func(arguments ...string) error {
		return gate.run(
			ctx,
			app,
			map[string]string{"GOWORK": "off", "GOPROXY": "off"},
			"go",
			append([]string{"tool", identity.CLITool, "generate"}, arguments...)...,
		)
	}
	if generateErr := generate(".", "./component"); generateErr != nil {
		return generateErr
	}
	first, snapshotErr := snapshotOwned(owned)
	if snapshotErr != nil {
		return snapshotErr
	}
	if len(first) == 0 {
		return errors.New("first generation produced no owned output")
	}
	if cleanupErr := cleanup(); cleanupErr != nil {
		return cleanupErr
	}
	if generateErr := generate(".", "./component"); generateErr != nil {
		return generateErr
	}
	second, snapshotErr := snapshotOwned(owned)
	if snapshotErr != nil {
		return snapshotErr
	}
	if !snapshotsEqual(first, second) {
		return errors.New("generation from zero is not byte-for-byte deterministic")
	}
	if generateErr := generate("--check", ".", "./component"); generateErr != nil {
		return generateErr
	}
	if err := gate.run(
		ctx,
		app,
		map[string]string{"GOWORK": "off", "GOPROXY": "off"},
		"go",
		"tool",
		identity.CLITool,
		"verify",
		".",
		"./component",
	); err != nil {
		return err
	}
	if err := gate.run(ctx, app, map[string]string{"GOWORK": "off", "GOPROXY": "off"}, "go", "mod", "tidy", "-diff"); err != nil {
		return err
	}
	if err := gate.run(ctx, app, map[string]string{"GOWORK": "off", "GOPROXY": "off"}, "go", "test", "./..."); err != nil {
		return err
	}
	if err := gate.run(
		ctx,
		app,
		map[string]string{"GOWORK": "off", "GOPROXY": "off"},
		"go",
		"tool",
		identity.CLITool,
		"build",
		".",
		"./component",
	); err != nil {
		return err
	}
	output, err := gate.capture(
		ctx,
		app,
		map[string]string{"GOWORK": "off", "GOPROXY": "off"},
		"go",
		"tool",
		identity.CLITool,
		"run",
		".",
		"./component",
		"--",
		"-check",
	)
	if err != nil {
		return err
	}
	if !bytes.Contains(output, []byte("ready.")) {
		return fmt.Errorf("fixture readiness output = %q", strings.TrimSpace(string(output)))
	}
	for path, digest := range second {
		if strings.HasSuffix(path, ".go") && digest == ([32]byte{}) {
			return fmt.Errorf("generated source %s has an empty digest", path)
		}
	}
	return nil
}

func (gate verifier) run(
	ctx context.Context,
	directory string,
	environment map[string]string,
	executable string,
	arguments ...string,
) error {
	_, err := gate.command(ctx, directory, environment, executable, arguments...)
	return err
}

func (gate verifier) capture(
	ctx context.Context,
	directory string,
	environment map[string]string,
	executable string,
	arguments ...string,
) ([]byte, error) {
	return gate.command(ctx, directory, environment, executable, arguments...)
}

func (gate verifier) command(
	ctx context.Context,
	directory string,
	environment map[string]string,
	executable string,
	arguments ...string,
) ([]byte, error) {
	if _, err := fmt.Fprintf(gate.output, "    %s %s\n", executable, strings.Join(arguments, " ")); err != nil {
		return nil, err
	}
	if gate.executeStreams != nil {
		stdout, stderr, err := gate.executeStreams(ctx, directory, environment, executable, arguments...)
		return commandResult(executable, arguments, stdout, stderr, err)
	}
	if gate.execute != nil {
		output, err := gate.execute(ctx, directory, environment, executable, arguments...)
		return commandResult(executable, arguments, output, nil, err)
	}
	command := exec.CommandContext(ctx, executable, arguments...) // #nosec G204 -- verifier commands are repository-owned constants.
	command.Dir = directory
	command.Env = mergedEnvironment(environment)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	return commandResult(executable, arguments, stdout.Bytes(), stderr.Bytes(), err)
}

func commandResult(executable string, arguments []string, stdout, stderr []byte, err error) ([]byte, error) {
	if err != nil {
		detail := bytes.TrimSpace(bytes.Join([][]byte{bytes.TrimSpace(stdout), bytes.TrimSpace(stderr)}, []byte("\n")))
		if len(detail) > maxCommandFailureOutput {
			detail = append([]byte("[command output truncated]\n"), detail[len(detail)-maxCommandFailureOutput:]...)
		}
		text := string(detail)
		if text == "" {
			return nil, fmt.Errorf("%s %s: %w", executable, strings.Join(arguments, " "), err)
		}
		return nil, fmt.Errorf("%s %s: %w\n%s", executable, strings.Join(arguments, " "), err, text)
	}
	return stdout, nil
}

func (gate verifier) removeTemporary(path string) {
	if err := os.RemoveAll(path); err != nil {
		if _, writeErr := fmt.Fprintf(
			gate.output,
			"warning: remove verifier scratch %s: %v\n",
			path,
			err,
		); writeErr != nil {
			return
		}
	}
}

func mergedEnvironment(overrides map[string]string) []string {
	values := make(map[string]string)
	keys := make(map[string]string)
	for _, item := range os.Environ() {
		name, value, found := strings.Cut(item, "=")
		if !found {
			continue
		}
		canonical := strings.ToUpper(name)
		keys[canonical] = name
		values[canonical] = value
	}
	for name, value := range overrides {
		canonical := strings.ToUpper(name)
		keys[canonical] = name
		values[canonical] = value
	}
	result := make([]string, 0, len(values))
	for canonical, value := range values {
		result = append(result, keys[canonical]+"="+value)
	}
	sort.Strings(result)
	return result
}

func executableSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func relativePath(root, path string) string {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(relative)
}

func snapshotOwned(roots []string) (map[string][32]byte, error) {
	result := make(map[string][32]byte)
	for _, root := range roots {
		files, err := snapshotTree(root)
		if err != nil {
			return nil, err
		}
		prefix := filepath.Base(root)
		for path, digest := range files {
			result[prefix+"/"+path] = digest
		}
	}
	return result, nil
}

func snapshotTree(root string) (map[string][32]byte, error) {
	result := make(map[string][32]byte)
	directory, err := os.OpenRoot(root)
	if errors.Is(err, fs.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	walkErr := fs.WalkDir(directory.FS(), ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("snapshot contains symlink %s", path)
		}
		content, err := directory.ReadFile(path)
		if err != nil {
			return err
		}
		result[filepath.ToSlash(path)] = sha256.Sum256(content)
		return nil
	})
	return result, errors.Join(walkErr, directory.Close())
}

func snapshotsEqual(left, right map[string][32]byte) bool {
	if len(left) != len(right) {
		return false
	}
	keys := make([]string, 0, len(left))
	for key := range left {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if !slices.Equal(keys, sortedKeys(right)) {
		return false
	}
	for _, key := range keys {
		if left[key] != right[key] {
			return false
		}
	}
	return true
}

func sortedKeys(values map[string][32]byte) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}
