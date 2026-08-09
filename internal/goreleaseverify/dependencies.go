package goreleaseverify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

const (
	authenticationModFile = ".spice-release-verify.mod"
	authenticationSumFile = ".spice-release-verify.sum"
)

type goRunner interface {
	Output(context.Context, string, []string, ...string) ([]byte, error)
}

type systemGoRunner struct {
	executable string
}

func newSystemGoRunner() (systemGoRunner, error) {
	located, err := exec.LookPath("go")
	if err != nil {
		return systemGoRunner{}, fmt.Errorf("locate Go executable: %w", err)
	}
	absolute, err := filepath.Abs(located)
	if err != nil {
		return systemGoRunner{}, fmt.Errorf("resolve Go executable: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return systemGoRunner{}, fmt.Errorf("resolve Go executable links: %w", err)
	}
	info, err := os.Lstat(resolved)
	if err != nil {
		return systemGoRunner{}, fmt.Errorf("inspect Go executable: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return systemGoRunner{}, fmt.Errorf("go executable %q is not a regular file", resolved)
	}
	return systemGoRunner{executable: resolved}, nil
}

func (runner systemGoRunner) Output(
	ctx context.Context,
	root string,
	environment []string,
	arguments ...string,
) ([]byte, error) {
	if len(arguments) == 0 {
		return nil, errors.New("go command requires an argument")
	}
	if runner.executable == "" {
		return nil, errors.New("go executable is not bound")
	}
	// #nosec G204 -- executable and every argument are fixed verifier policy.
	command := exec.CommandContext(ctx, runner.executable, arguments...)
	command.Dir = root
	command.Env = environment
	var stdout, stderr boundedBuffer
	stdout.maximum = maxModuleGraph
	stderr.maximum = maxDiagnostic
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("go %s: %w: %s", arguments[0], err, stderr.String())
	}
	if stdout.truncated {
		return nil, fmt.Errorf("go %s output exceeds limits", arguments[0])
	}
	return slices.Clone(stdout.Bytes()), nil
}

func authenticateVendorAndBuild(
	ctx context.Context,
	workspace isolatedWorkspace,
	source sourceIdentity,
	runner goRunner,
) error {
	if runner == nil {
		return errors.New("authenticate Go module: runner is nil")
	}
	goMod, err := readWorkspaceFile(workspace.source, "go.mod", maxModuleGraph)
	if err != nil {
		return err
	}
	goSum, err := readWorkspaceFile(workspace.source, "go.sum", maxModuleGraph)
	if err != nil {
		return err
	}
	if writeErr := writeAuthenticationFile(workspace.source, authenticationModFile, goMod); writeErr != nil {
		return writeErr
	}
	if writeErr := writeAuthenticationFile(workspace.source, authenticationSumFile, goSum); writeErr != nil {
		return writeErr
	}
	online := trustedGoEnvironment(os.Environ(), workspace, true)
	version, err := runner.Output(ctx, workspace.source, online, "version")
	if err != nil {
		return err
	}
	fields := strings.Fields(string(version))
	if len(fields) < 3 || fields[2] != "go1.26.5" {
		return fmt.Errorf("go release verifier requires go1.26.5, got %q", strings.TrimSpace(string(version)))
	}
	if _, err := runner.Output(
		ctx,
		workspace.source,
		online,
		"mod",
		"download",
		"-modfile="+authenticationModFile,
		"all",
	); err != nil {
		return fmt.Errorf("authenticate selected modules with public Go checksum database: %w", err)
	}
	if _, err := runner.Output(ctx, workspace.source, online, "mod", "verify"); err != nil {
		return fmt.Errorf("verify authenticated isolated module cache: %w", err)
	}
	generatedVendor := filepath.Join(workspace.base, "regenerated-vendor")
	if _, err := runner.Output(
		ctx,
		workspace.source,
		online,
		"mod",
		"vendor",
		"-modfile="+authenticationModFile,
		"-o",
		generatedVendor,
	); err != nil {
		return fmt.Errorf("regenerate vendor from authenticated modules: %w", err)
	}
	if err := requireWorkspaceFile(workspace.source, authenticationModFile, goMod); err != nil {
		return err
	}
	for _, name := range []string{authenticationModFile, authenticationSumFile} {
		if err := os.Remove(filepath.Join(workspace.source, name)); err != nil {
			return fmt.Errorf("remove private dependency authentication file %q: %w", name, err)
		}
	}
	if err := requireWorkspaceFile(workspace.source, "go.mod", goMod); err != nil {
		return err
	}
	if err := requireWorkspaceFile(workspace.source, "go.sum", goSum); err != nil {
		return err
	}
	if err := compareAuthenticatedVendor(source, workspace.source, generatedVendor); err != nil {
		return err
	}
	offline := trustedGoEnvironment(os.Environ(), workspace, false)
	if _, err := runner.Output(ctx, workspace.source, offline, "list", "-mod=vendor", "./..."); err != nil {
		return fmt.Errorf("list authenticated vendor-only module: %w", err)
	}
	if _, err := runner.Output(ctx, workspace.source, offline, "build", "-mod=vendor", "-trimpath", "./..."); err != nil {
		return fmt.Errorf("build authenticated vendor-only module with trimpath: %w", err)
	}
	return nil
}

func writeAuthenticationFile(root, name string, content []byte) (resultErr error) {
	if filepath.Base(name) != name || name == "." || name == ".." {
		return fmt.Errorf("private dependency authentication file name %q is unsafe", name)
	}
	directory, err := os.OpenRoot(root)
	if err != nil {
		return fmt.Errorf("open private dependency authentication directory: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, directory.Close()) }()
	file, err := directory.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create private dependency authentication file %q: %w", name, err)
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	written, err := file.Write(content)
	if err != nil {
		return fmt.Errorf("write private dependency authentication file %q: %w", name, err)
	}
	if written != len(content) {
		return fmt.Errorf("write private dependency authentication file %q: %w", name, io.ErrShortWrite)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync private dependency authentication file %q: %w", name, err)
	}
	return nil
}

func trustedGoEnvironment(source []string, workspace isolatedWorkspace, online bool) []string {
	result := make([]string, 0, len(source)+16)
	for _, entry := range source {
		key, _, found := strings.Cut(entry, "=")
		if !found || blockGoEnvironment(strings.ToUpper(key)) {
			continue
		}
		result = append(result, entry)
	}
	proxy := "off"
	sumDatabase := "off"
	if online {
		proxy = "https://proxy.golang.org"
		sumDatabase = "sum.golang.org"
	}
	return append(
		result,
		"CGO_ENABLED=0",
		"GO111MODULE=on",
		"GOAUTH=off",
		"GOCACHE="+workspace.buildCache,
		"GOENV=off",
		"GOFLAGS=-mod=readonly",
		"GOPATH="+workspace.goPath,
		"GOMODCACHE="+workspace.moduleCache,
		"GONOPROXY=none",
		"GONOSUMDB=none",
		"GOPRIVATE=none",
		"GOPROXY="+proxy,
		"GOSUMDB="+sumDatabase,
		"GOTELEMETRY=off",
		"GOTMPDIR="+workspace.temporary,
		"GOTOOLCHAIN=local",
		"GOWORK=off",
		"TEMP="+workspace.temporary,
		"TMP="+workspace.temporary,
		"TMPDIR="+workspace.temporary,
	)
}

func blockGoEnvironment(key string) bool {
	if strings.HasPrefix(key, "GO") || strings.HasPrefix(key, "CGO_") {
		return true
	}
	switch key {
	case "ALL_PROXY", "AR", "CC", "CXX", "FC", "HTTP_PROXY", "HTTPS_PROXY",
		"LD", "NETRC", "NM", "NO_PROXY", "PKG_CONFIG", "RANLIB", "TEMP", "TMP", "TMPDIR":
		return true
	default:
		return strings.HasPrefix(key, "GIT_") || strings.HasPrefix(key, "SSH_")
	}
}

type vendorFile struct {
	content []byte
	mode    string
}

func compareAuthenticatedVendor(
	source sourceIdentity,
	materializedSource string,
	generatedVendor string,
) error {
	committed, err := committedVendorFiles(source, materializedSource)
	if err != nil {
		return err
	}
	generated, err := filesystemVendorFiles(generatedVendor)
	if err != nil {
		return err
	}
	committedNames := sortedVendorNames(committed)
	generatedNames := sortedVendorNames(generated)
	if !slices.Equal(committedNames, generatedNames) {
		return fmt.Errorf(
			"committed vendor file set differs from authenticated regeneration: committed %v, regenerated %v",
			committedNames,
			generatedNames,
		)
	}
	for _, name := range committedNames {
		want := committed[name]
		actual := generated[name]
		if want.mode != actual.mode || !bytes.Equal(want.content, actual.content) {
			return fmt.Errorf("committed vendor file %q differs in bytes or mode from authenticated regeneration", name)
		}
	}
	return nil
}

func committedVendorFiles(source sourceIdentity, materializedSource string) (map[string]vendorFile, error) {
	result := make(map[string]vendorFile)
	for _, entry := range source.entries {
		name, found := strings.CutPrefix(entry.name, "vendor/")
		if !found {
			continue
		}
		content, err := readWorkspaceFile(materializedSource, entry.name, maxArtifactBytes)
		if err != nil {
			return nil, err
		}
		result[name] = vendorFile{content: content, mode: entry.mode}
	}
	if len(result) == 0 || len(result) > maxVendorFiles {
		return nil, errors.New("committed vendor tree is empty or exceeds file limits")
	}
	return result, nil
}

func filesystemVendorFiles(root string) (map[string]vendorFile, error) {
	rootInfo, rootErr := os.Lstat(root)
	if rootErr != nil {
		return nil, fmt.Errorf("authenticated regenerated vendor root is not a real directory: %w", rootErr)
	}
	if !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("authenticated regenerated vendor root is not a real directory")
	}
	vendorRoot, openErr := os.OpenRoot(root)
	if openErr != nil {
		return nil, fmt.Errorf("open authenticated regenerated vendor root: %w", openErr)
	}
	result := make(map[string]vendorFile)
	portable := make(map[string]string)
	var total int64
	traversalErr := fs.WalkDir(vendorRoot.FS(), ".", func(filename string, entry fs.DirEntry, entryErr error) error {
		if entryErr != nil {
			return entryErr
		}
		if filename == "." || entry.IsDir() {
			return nil
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("authenticated regenerated vendor path %q is not a regular file", filename)
		}
		relative := filepath.ToSlash(filename)
		if pathErr := validatePortablePath(relative); pathErr != nil {
			return pathErr
		}
		key := strings.ToLower(relative)
		if prior, duplicate := portable[key]; duplicate {
			return fmt.Errorf("authenticated vendor paths %q and %q collide", prior, relative)
		}
		portable[key] = relative
		if len(result) >= maxVendorFiles || info.Size() < 0 || total > maxVendorBytes-info.Size() {
			return errors.New("authenticated regenerated vendor tree exceeds limits")
		}
		content, readErr := readVendorFile(vendorRoot, relative, info)
		if readErr != nil {
			return readErr
		}
		total += info.Size()
		mode := "100644"
		if info.Mode().Perm()&0o111 != 0 {
			mode = "100755"
		}
		result[relative] = vendorFile{content: content, mode: mode}
		return nil
	})
	closeErr := vendorRoot.Close()
	if traversalErr != nil {
		return nil, fmt.Errorf("inspect authenticated regenerated vendor tree: %w", traversalErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close authenticated regenerated vendor root: %w", closeErr)
	}
	return result, nil
}

func readVendorFile(root *os.Root, name string, expected fs.FileInfo) (result []byte, resultErr error) {
	file, err := root.Open(filepath.FromSlash(name))
	if err != nil {
		return nil, fmt.Errorf("open authenticated vendor file %q: %w", name, err)
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	opened, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect authenticated vendor file %q: %w", name, err)
	}
	if !opened.Mode().IsRegular() || opened.Size() != expected.Size() || !os.SameFile(opened, expected) {
		return nil, fmt.Errorf("authenticated vendor file %q changed while opening", name)
	}
	content, err := io.ReadAll(io.LimitReader(file, maxVendorBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read authenticated vendor file %q: %w", name, err)
	}
	if int64(len(content)) > maxVendorBytes {
		return nil, fmt.Errorf("authenticated vendor file %q exceeds limits", name)
	}
	return content, nil
}

func sortedVendorNames(files map[string]vendorFile) []string {
	result := make([]string, 0, len(files))
	for name := range files {
		result = append(result, name)
	}
	slices.Sort(result)
	return result
}

func readWorkspaceFile(rootDirectory, name string, maximum int64) (result []byte, resultErr error) {
	root, err := os.OpenRoot(rootDirectory)
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	info, err := root.Lstat(filepath.FromSlash(name))
	if err != nil {
		return nil, fmt.Errorf("stat isolated source file %q: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("isolated source file %q has unsafe mode %s", name, info.Mode())
	}
	if info.Size() < 0 || info.Size() > maximum {
		return nil, fmt.Errorf(
			"isolated source file %q has size %d; require at most %d bytes",
			name,
			info.Size(),
			maximum,
		)
	}
	file, err := root.Open(filepath.FromSlash(name))
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	content, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, fmt.Errorf("read isolated source file %q: %w", name, err)
	}
	if int64(len(content)) > maximum {
		return nil, fmt.Errorf("isolated source file %q grew beyond %d bytes while reading", name, maximum)
	}
	return content, nil
}

func requireWorkspaceFile(root, name string, expected []byte) error {
	actual, err := readWorkspaceFile(root, name, maxModuleGraph)
	if err != nil {
		return err
	}
	if !bytes.Equal(actual, expected) {
		return fmt.Errorf("isolated source file %q changed during dependency authentication", name)
	}
	return nil
}
