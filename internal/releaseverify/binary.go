package releaseverify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/buildinfo"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"runtime"
	"slices"
	"strings"
	"time"
)

const requiredGoVersion = "go1.26.5"

type releaseTarget struct {
	goos    string
	goarch  string
	windows bool
}

func releaseTargets() []releaseTarget {
	return []releaseTarget{
		{goos: "darwin", goarch: "amd64"},
		{goos: "darwin", goarch: "arm64"},
		{goos: "linux", goarch: "amd64"},
		{goos: "linux", goarch: "arm64"},
		{goos: "windows", goarch: "amd64", windows: true},
		{goos: "windows", goarch: "arm64", windows: true},
	}
}

func (target releaseTarget) archiveName(versionName string) string {
	extension := ".tar.gz"
	if target.windows {
		extension = ".zip"
	}
	return fmt.Sprintf(
		"spice_%s_%s_%s%s",
		versionName,
		target.goos,
		target.goarch,
		extension,
	)
}

func (target releaseTarget) executableName() string {
	if target.windows {
		return "spice.exe"
	}
	return "spice"
}

func verifyBinaryArchive(
	ctx context.Context,
	entries []archiveEntry,
	target releaseTarget,
	versionName string,
	version string,
	license []byte,
	readme []byte,
	expectedDependencies map[string]string,
) error {
	base := strings.TrimSuffix(
		target.archiveName(versionName),
		map[bool]string{true: ".zip", false: ".tar.gz"}[target.windows],
	)
	expected := []struct {
		name string
		mode os.FileMode
	}{
		{name: path.Join(base, target.executableName()), mode: 0o755},
		{name: path.Join(base, "LICENSE"), mode: 0o644},
		{name: path.Join(base, "README.md"), mode: 0o644},
	}
	if len(entries) != len(expected) {
		return fmt.Errorf("binary archive has %d entries, require %d", len(entries), len(expected))
	}
	for index, want := range expected {
		if entries[index].name != want.name || entries[index].mode != want.mode ||
			entries[index].linkTarget != "" {
			return fmt.Errorf("binary archive entry %d has unexpected path or mode", index)
		}
	}
	if len(entries[0].data) == 0 || !bytes.Equal(entries[1].data, license) ||
		!bytes.Equal(entries[2].data, readme) {
		return errors.New("binary archive payload does not match trusted release source")
	}
	if err := verifyBuildInfo(entries[0].data, target, version, expectedDependencies); err != nil {
		return err
	}
	if target.goos == "linux" && target.goarch == "amd64" {
		return executeLinuxAMD64(ctx, entries[0].data, version)
	}
	return nil
}

func verifyBuildInfo(
	binary []byte,
	target releaseTarget,
	version string,
	expectedDependencies map[string]string,
) error {
	info, err := buildinfo.Read(bytes.NewReader(binary))
	if err != nil {
		return fmt.Errorf("read Spice binary build info: %w", err)
	}
	if info.GoVersion != requiredGoVersion {
		return fmt.Errorf("binary Go version is %q, require %q", info.GoVersion, requiredGoVersion)
	}
	if info.Path != modulePath+"/cmd/spice" || info.Main.Path != modulePath {
		return fmt.Errorf(
			"binary identity is path %q in module %q",
			info.Path,
			info.Main.Path,
		)
	}
	if info.Main.Replace != nil {
		return errors.New("binary main module has a replacement")
	}
	settings := make(map[string]string, len(info.Settings))
	for _, setting := range info.Settings {
		if _, duplicate := settings[setting.Key]; duplicate {
			return fmt.Errorf("binary build setting %q is duplicated", setting.Key)
		}
		settings[setting.Key] = setting.Value
	}
	expectedSettings := map[string]string{
		"-buildmode":  "exe",
		"-compiler":   "gc",
		"-trimpath":   "true",
		"CGO_ENABLED": "0",
		"GOARCH":      target.goarch,
		"GOOS":        target.goos,
	}
	if target.goarch == "amd64" {
		expectedSettings["GOAMD64"] = "v1"
	} else {
		expectedSettings["GOARM64"] = "v8.0"
	}
	for key, want := range expectedSettings {
		if settings[key] != want {
			return fmt.Errorf("binary build setting %s is %q, require %q", key, settings[key], want)
		}
	}
	if len(settings) != len(expectedSettings) {
		keys := make([]string, 0, len(settings))
		for key := range settings {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		return fmt.Errorf("binary has unexpected build settings %v", keys)
	}
	if info.Main.Version != "(devel)" && info.Main.Version != version {
		return fmt.Errorf("binary main module version is %q", info.Main.Version)
	}
	// Go does not expose -ldflags in debug.BuildInfo. The builder's linked CLI
	// version is nevertheless retained as a literal even in stripped binaries.
	if !bytes.Contains(binary, []byte(version)) {
		return fmt.Errorf("binary does not contain exact linked version %q", version)
	}
	actualDependencies := make(map[string]string, len(info.Deps))
	for _, dependency := range info.Deps {
		if dependency.Replace != nil {
			return fmt.Errorf("binary dependency %q has a replacement", dependency.Path)
		}
		if _, duplicate := actualDependencies[dependency.Path]; duplicate {
			return fmt.Errorf("binary dependency %q is duplicated", dependency.Path)
		}
		actualDependencies[dependency.Path] = dependency.Version
	}
	if len(actualDependencies) != len(expectedDependencies) {
		return fmt.Errorf(
			"binary dependency graph has %d modules, require %d",
			len(actualDependencies),
			len(expectedDependencies),
		)
	}
	for module, version := range expectedDependencies {
		if actualDependencies[module] != version {
			return fmt.Errorf(
				"binary dependency %s is %q, require %q",
				module,
				actualDependencies[module],
				version,
			)
		}
	}
	return nil
}

func verifyBinaryArtifacts(
	ctx context.Context,
	directory string,
	digests map[string][sha256.Size]byte,
	versionName string,
	version string,
	epoch time.Time,
	license []byte,
	readme []byte,
	source []sourceEntry,
) (resultErr error) {
	if err := validateVerifierGo(ctx); err != nil {
		return err
	}
	sourceRoot, err := materializeTrustedSource(source)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, os.RemoveAll(sourceRoot))
	}()
	for _, target := range releaseTargets() {
		expectedDependencies, listErr := listBinaryModules(ctx, sourceRoot, target)
		if listErr != nil {
			return listErr
		}
		name := target.archiveName(versionName)
		archiveData, readErr := readAuthenticatedArtifact(
			ctx,
			directory,
			name,
			digests[name],
		)
		if readErr != nil {
			return readErr
		}
		entries, archiveErr := readArchive(ctx, archiveData, target.windows, epoch)
		if archiveErr != nil {
			return fmt.Errorf("verify %s: %w", name, archiveErr)
		}
		if binaryErr := verifyBinaryArchive(
			ctx,
			entries,
			target,
			versionName,
			version,
			license,
			readme,
			expectedDependencies,
		); binaryErr != nil {
			return fmt.Errorf("verify %s: %w", name, binaryErr)
		}
	}
	return nil
}

func validateVerifierGo(ctx context.Context) error {
	command := exec.CommandContext(ctx, "go", "env", "GOVERSION")
	command.Env = goListEnvironment(releaseTarget{})
	var stdout, stderr boundedOutput
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return fmt.Errorf("validate verifier Go toolchain: %w", contextErr)
		}
		return fmt.Errorf("validate verifier Go toolchain: %w: %s", err, stderr.data.String())
	}
	if stdout.overflow || stderr.overflow {
		return errors.New("validate verifier Go toolchain: output exceeded limits")
	}
	if got := strings.TrimSpace(stdout.data.String()); got != requiredGoVersion {
		return fmt.Errorf("verify release: require %s Go toolchain, got %q", requiredGoVersion, got)
	}
	return nil
}

func materializeTrustedSource(entries []sourceEntry) (result string, resultErr error) {
	directory, err := os.MkdirTemp("", "spice-release-source-verify-*")
	if err != nil {
		return "", fmt.Errorf("create private trusted source directory: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			resultErr = errors.Join(resultErr, os.RemoveAll(directory))
		}
	}()
	// #nosec G302 -- the private directory needs owner search permission for Go tooling.
	if chmodErr := os.Chmod(directory, 0o700); chmodErr != nil {
		return "", fmt.Errorf("make trusted source directory private: %w", chmodErr)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return "", fmt.Errorf("open trusted source directory: %w", err)
	}
	for _, entry := range entries {
		directoryName := path.Dir(entry.path)
		if directoryName != "." {
			if err := root.MkdirAll(directoryName, 0o700); err != nil {
				return "", errors.Join(
					fmt.Errorf("create trusted source directory %q: %w", directoryName, err),
					root.Close(),
				)
			}
		}
	}
	for _, entry := range entries {
		if entry.linkTarget != "" {
			if err := root.Symlink(entry.linkTarget, entry.path); err != nil {
				return "", errors.Join(
					fmt.Errorf("materialize trusted source symlink %q: %w", entry.path, err),
					root.Close(),
				)
			}
			continue
		}
		file, err := root.OpenFile(
			entry.path,
			os.O_WRONLY|os.O_CREATE|os.O_EXCL,
			entry.mode,
		)
		if err != nil {
			return "", errors.Join(
				fmt.Errorf("create trusted source file %q: %w", entry.path, err),
				root.Close(),
			)
		}
		_, writeErr := file.Write(entry.data)
		if err := errors.Join(writeErr, file.Close()); err != nil {
			return "", errors.Join(
				fmt.Errorf("write trusted source file %q: %w", entry.path, err),
				root.Close(),
			)
		}
	}
	if err := root.Close(); err != nil {
		return "", fmt.Errorf("close trusted source directory: %w", err)
	}
	committed = true
	return directory, nil
}

func listBinaryModules(
	ctx context.Context,
	root string,
	target releaseTarget,
) (map[string]string, error) {
	template := `{{with .Module}}{{.Path}}{{"\t"}}{{.Version}}{{"\t"}}{{if .Replace}}replace{{end}}{{end}}`
	// #nosec G204 -- executable, package, and template are fixed; target is from a closed matrix.
	command := exec.CommandContext(
		ctx,
		"go",
		"list",
		"-mod=vendor",
		"-deps",
		"-f",
		template,
		"./cmd/spice",
	)
	command.Dir = root
	command.Env = goListEnvironment(target)
	var stdout, stderr boundedOutput
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, fmt.Errorf("list trusted binary dependencies: %w", contextErr)
		}
		return nil, fmt.Errorf("list trusted binary dependencies: %w: %s", err, stderr.data.String())
	}
	if stdout.overflow || stderr.overflow {
		return nil, errors.New("trusted binary dependency listing exceeded output limits")
	}
	modules := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSuffix(stdout.data.String(), "\n"), "\n") {
		line = strings.TrimSuffix(line, "\r")
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 3 || fields[0] == "" || fields[2] != "" {
			return nil, fmt.Errorf("trusted binary dependency listing has invalid line %q", line)
		}
		if fields[0] == modulePath {
			continue
		}
		if fields[1] == "" {
			return nil, fmt.Errorf("trusted binary dependency %q has no version", fields[0])
		}
		if prior, found := modules[fields[0]]; found && prior != fields[1] {
			return nil, fmt.Errorf("trusted binary dependency %q has conflicting versions", fields[0])
		}
		modules[fields[0]] = fields[1]
	}
	return modules, nil
}

func goListEnvironment(target releaseTarget) []string {
	environment := make([]string, 0, len(os.Environ())+16)
	for _, value := range os.Environ() {
		name, _, _ := strings.Cut(value, "=")
		switch strings.ToUpper(name) {
		case "CGO_ENABLED", "GO111MODULE", "GO386", "GOAMD64", "GOARCH", "GOARM", "GOARM64",
			"GODEBUG", "GOENV", "GOEXPERIMENT", "GOFIPS140", "GOFLAGS",
			"GOMIPS", "GOMIPS64", "GONOPROXY", "GONOSUMDB", "GOOS",
			"GOPPC64", "GOPRIVATE", "GOPROXY", "GORISCV64", "GOSUMDB",
			"GOTOOLCHAIN", "GOWASM", "GOWORK":
			continue
		default:
			environment = append(environment, value)
		}
	}
	environment = append(
		environment,
		"CGO_ENABLED=0",
		"GO111MODULE=on",
		"GODEBUG=",
		"GOENV=off",
		"GOEXPERIMENT=",
		"GOFIPS140=off",
		"GOFLAGS=",
		"GONOPROXY=",
		"GONOSUMDB=",
		"GOPRIVATE=",
		"GOPROXY=off",
		"GOSUMDB=off",
		"GOTOOLCHAIN=local",
		"GOWORK=off",
	)
	if target.goos != "" {
		environment = append(environment, "GOOS="+target.goos)
	}
	if target.goarch != "" {
		environment = append(environment, "GOARCH="+target.goarch)
	}
	switch target.goarch {
	case "amd64":
		environment = append(environment, "GOAMD64=v1")
	case "arm64":
		environment = append(environment, "GOARM64=v8.0")
	}
	return environment
}

const maxExecutedOutputBytes = 32 << 10

type boundedOutput struct {
	data     bytes.Buffer
	overflow bool
}

func (output *boundedOutput) Write(data []byte) (int, error) {
	available := maxExecutedOutputBytes - output.data.Len()
	if available > 0 {
		_, _ = output.data.Write(data[:min(len(data), available)])
	}
	if len(data) > available {
		output.overflow = true
	}
	return len(data), nil
}

func executeLinuxAMD64(
	ctx context.Context,
	binary []byte,
	version string,
) (resultErr error) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		return nil
	}
	directory, err := os.MkdirTemp("", "spice-release-verify-*")
	if err != nil {
		return fmt.Errorf("create private binary verification directory: %w", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, os.RemoveAll(directory))
	}()
	// #nosec G302 -- the private directory needs owner search permission to execute the verified binary.
	if chmodErr := os.Chmod(directory, 0o700); chmodErr != nil {
		return fmt.Errorf("make binary verification directory private: %w", chmodErr)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return fmt.Errorf("open binary verification directory: %w", err)
	}
	file, err := root.OpenFile("spice", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
	if err != nil {
		return errors.Join(fmt.Errorf("create verified Spice binary: %w", err), root.Close())
	}
	_, writeErr := file.Write(binary)
	closeErr := file.Close()
	rootCloseErr := root.Close()
	if err := errors.Join(writeErr, closeErr, rootCloseErr); err != nil {
		return fmt.Errorf("write verified Spice binary: %w", err)
	}
	executionCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// #nosec G204 -- this executes authenticated bytes from the signed release in a private directory.
	command := exec.CommandContext(executionCtx, path.Join(directory, "spice"), "version")
	command.Dir = directory
	command.Env = []string{
		"HOME=" + directory,
		"LANG=C",
		"LC_ALL=C",
		"TMPDIR=" + directory,
	}
	var stdout, stderr boundedOutput
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		if contextErr := executionCtx.Err(); contextErr != nil {
			return fmt.Errorf("execute verified Spice binary: %w", contextErr)
		}
		return fmt.Errorf("execute verified Spice binary: %w: %s", err, stderr.data.String())
	}
	if stdout.overflow || stderr.overflow {
		return errors.New("verified Spice binary exceeded output limits")
	}
	want := "spice " + version + "\n"
	if stdout.data.String() != want || stderr.data.Len() != 0 {
		return fmt.Errorf(
			"verified Spice binary output is stdout %q stderr %q, require %q",
			stdout.data.String(),
			stderr.data.String(),
			want,
		)
	}
	return nil
}

var _ io.Writer = (*boundedOutput)(nil)
