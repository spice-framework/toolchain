package release

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

const maxDiagnosticBytes = 32 << 10

// Config describes one guarded release build.
type Config struct {
	Root          string
	OutputDir     string
	Version       string
	Epoch         time.Time
	Targets       []Target
	PrivateKey    []byte
	AllowUnsigned bool
}

// Result describes the committed release directory.
type Result struct {
	OutputDir string
	Files     []string
}

// Build creates a new deterministic release directory without overwriting an
// existing path or consulting the network.
func Build(
	ctx context.Context,
	config Config,
) (result Result, resultErr error) {
	normalized, err := normalizeConfig(ctx, config)
	if err != nil {
		return Result{}, err
	}
	parent := filepath.Dir(normalized.OutputDir)
	if mkdirErr := os.MkdirAll(parent, 0o750); mkdirErr != nil {
		return Result{}, fmt.Errorf(
			"create release parent directory: %w",
			mkdirErr,
		)
	}
	staging, err := os.MkdirTemp(parent, ".spice-release-*")
	if err != nil {
		return Result{}, fmt.Errorf("create release staging directory: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			if cleanupErr := os.RemoveAll(staging); cleanupErr != nil {
				resultErr = errors.Join(
					resultErr,
					fmt.Errorf(
						"remove release staging directory: %w",
						cleanupErr,
					),
				)
			}
		}
	}()

	files, err := buildArtifacts(ctx, normalized, staging)
	if err != nil {
		return Result{}, err
	}
	if err := os.Rename(staging, normalized.OutputDir); err != nil {
		return Result{}, fmt.Errorf(
			"commit release directory %q: %w",
			normalized.OutputDir,
			err,
		)
	}
	committed = true
	return Result{OutputDir: normalized.OutputDir, Files: files}, nil
}

func normalizeConfig(ctx context.Context, config Config) (Config, error) {
	if err := validateConfig(ctx, config); err != nil {
		return Config{}, err
	}
	root, err := filepath.Abs(config.Root)
	if err != nil {
		return Config{}, fmt.Errorf("resolve release root: %w", err)
	}
	output, err := filepath.Abs(config.OutputDir)
	if err != nil {
		return Config{}, fmt.Errorf("resolve release output directory: %w", err)
	}
	if pathErr := validateReleasePaths(root, output); pathErr != nil {
		return Config{}, pathErr
	}
	targets, err := normalizeTargets(config.Targets)
	if err != nil {
		return Config{}, err
	}
	config.Root = root
	config.OutputDir = output
	config.Epoch = config.Epoch.UTC().Truncate(time.Second)
	config.Targets = targets
	config.PrivateKey = append([]byte(nil), config.PrivateKey...)
	return config, nil
}

func validateConfig(ctx context.Context, config Config) error {
	if ctx == nil {
		return fmt.Errorf("build release: context is nil")
	}
	if !semver.IsValid(config.Version) {
		return fmt.Errorf(
			"build release: version %q is not canonical semantic version",
			config.Version,
		)
	}
	if config.Epoch.IsZero() {
		return fmt.Errorf("build release: source epoch is required")
	}
	if len(config.PrivateKey) == 0 && !config.AllowUnsigned {
		return fmt.Errorf(
			"build release: Ed25519 signing key is required unless unsigned mode is explicit",
		)
	}
	return nil
}

func validateReleasePaths(root, output string) error {
	if _, err := os.Stat(output); err == nil {
		return fmt.Errorf(
			"build release: output directory %q already exists",
			output,
		)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect release output directory: %w", err)
	}
	for _, name := range []string{"go.mod", "LICENSE", "README.md"} {
		if _, err := os.Stat(filepath.Join(root, name)); err != nil {
			return fmt.Errorf(
				"build release: required root file %q: %w",
				name,
				err,
			)
		}
	}
	return nil
}

func normalizeTargets(configured []Target) ([]Target, error) {
	targets := append([]Target(nil), configured...)
	if len(targets) == 0 {
		targets = DefaultTargets()
	}
	for _, target := range targets {
		if !slices.Contains(DefaultTargets(), target) {
			return nil, fmt.Errorf(
				"build release: unsupported target %q",
				target,
			)
		}
	}
	slices.SortFunc(targets, func(left, right Target) int {
		return strings.Compare(left.String(), right.String())
	})
	for index := 1; index < len(targets); index++ {
		if targets[index] == targets[index-1] {
			return nil, fmt.Errorf(
				"build release: duplicate target %q",
				targets[index],
			)
		}
	}
	return targets, nil
}

func buildArtifacts(
	ctx context.Context,
	config Config,
	staging string,
) ([]string, error) {
	license, err := readScopedFile(config.Root, "LICENSE")
	if err != nil {
		return nil, fmt.Errorf("read release license: %w", err)
	}
	readme, err := readScopedFile(config.Root, "README.md")
	if err != nil {
		return nil, fmt.Errorf("read release README: %w", err)
	}
	var files []string
	versionName := strings.TrimPrefix(config.Version, "v")
	for _, target := range config.Targets {
		binary, buildErr := buildBinary(ctx, config, staging, target)
		if buildErr != nil {
			return nil, buildErr
		}
		archiveName := fmt.Sprintf(
			"spice_%s_%s_%s%s",
			versionName,
			target.GOOS,
			target.GOARCH,
			target.ArchiveExtension(),
		)
		base := strings.TrimSuffix(
			archiveName,
			target.ArchiveExtension(),
		)
		entries := []archiveEntry{
			{
				name: base + "/" + target.ExecutableName(),
				mode: 0o755,
				data: binary,
			},
			{name: base + "/LICENSE", mode: 0o644, data: license},
			{name: base + "/README.md", mode: 0o644, data: readme},
		}
		if archiveErr := writeArchive(
			filepath.Join(staging, archiveName),
			target,
			config.Epoch,
			entries,
		); archiveErr != nil {
			return nil, archiveErr
		}
		files = append(files, archiveName)
	}
	sbomName := "spice_" + versionName + "_sbom.spdx.json"
	sbom, err := buildSBOM(
		ctx,
		config.Root,
		config.Version,
		config.Epoch,
	)
	if err != nil {
		return nil, err
	}
	if writeErr := writeNewFile(
		filepath.Join(staging, sbomName),
		sbom,
	); writeErr != nil {
		return nil, writeErr
	}
	files = append(files, sbomName)
	slices.Sort(files)
	checksums, err := artifactChecksums(staging, files)
	if err != nil {
		return nil, err
	}
	if err := writeNewFile(
		filepath.Join(staging, "checksums.txt"),
		checksums,
	); err != nil {
		return nil, err
	}
	files = append(files, "checksums.txt")
	if len(config.PrivateKey) != 0 {
		signature, publicKey, err := signChecksums(
			checksums,
			config.PrivateKey,
		)
		if err != nil {
			return nil, err
		}
		if err := writeNewFile(
			filepath.Join(staging, "checksums.txt.sig"),
			signature,
		); err != nil {
			return nil, err
		}
		if err := writeNewFile(
			filepath.Join(staging, "checksums.txt.pem"),
			publicKey,
		); err != nil {
			return nil, err
		}
		files = append(files, "checksums.txt.pem", "checksums.txt.sig")
	}
	slices.Sort(files)
	return files, nil
}

func buildBinary(
	ctx context.Context,
	config Config,
	staging string,
	target Target,
) ([]byte, error) {
	binaryPath := filepath.Join(
		staging,
		".binary-"+target.GOOS+"-"+target.GOARCH,
	)
	ldflags := "-s -w -X github.com/spice-framework/toolchain/internal/cli.Version=" +
		config.Version
	// #nosec G204 -- command is fixed; version is canonical semver, target is
	// allowlisted, and exec.CommandContext never invokes a shell.
	command := exec.CommandContext(
		ctx,
		"go",
		"build",
		"-mod=vendor",
		"-trimpath",
		"-buildvcs=false",
		"-ldflags="+ldflags,
		"-o",
		binaryPath,
		"./cmd/spice",
	)
	command.Dir = config.Root
	command.Env = releaseEnvironment(target.GOOS, target.GOARCH)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf(
			"build Spice CLI for %s: %w: %s",
			target,
			err,
			boundedText(stderr.Bytes()),
		)
	}
	binaryName := filepath.Base(binaryPath)
	binary, err := readScopedFile(staging, binaryName)
	if err != nil {
		return nil, fmt.Errorf("read Spice CLI for %s: %w", target, err)
	}
	stagingRoot, err := os.OpenRoot(staging)
	if err != nil {
		return nil, fmt.Errorf("open release staging root: %w", err)
	}
	removeErr := stagingRoot.Remove(binaryName)
	closeErr := stagingRoot.Close()
	if err := errors.Join(removeErr, closeErr); err != nil {
		return nil, fmt.Errorf("remove staged Spice CLI for %s: %w", target, err)
	}
	return binary, nil
}

func releaseEnvironment(goos, goarch string) []string {
	environment := make([]string, 0, len(os.Environ())+5)
	for _, value := range os.Environ() {
		name, _, _ := strings.Cut(value, "=")
		switch strings.ToUpper(name) {
		case "CGO_ENABLED", "GOARCH", "GOOS", "GOPROXY", "GOTOOLCHAIN":
			continue
		default:
			environment = append(environment, value)
		}
	}
	environment = append(
		environment,
		"CGO_ENABLED=0",
		"GOPROXY=off",
		"GOTOOLCHAIN=local",
	)
	if goos != "" {
		environment = append(environment, "GOOS="+goos)
	}
	if goarch != "" {
		environment = append(environment, "GOARCH="+goarch)
	}
	return environment
}

func artifactChecksums(root string, files []string) ([]byte, error) {
	var result strings.Builder
	for _, name := range files {
		data, err := readScopedFile(root, name)
		if err != nil {
			return nil, fmt.Errorf("read release artifact %q: %w", name, err)
		}
		sum := sha256.Sum256(data)
		fmt.Fprintf(&result, "%s  %s\n", hex.EncodeToString(sum[:]), name)
	}
	return []byte(result.String()), nil
}

func writeNewFile(filename string, data []byte) error {
	root, err := os.OpenRoot(filepath.Dir(filename))
	if err != nil {
		return fmt.Errorf("open release artifact root: %w", err)
	}
	file, err := root.OpenFile(
		filepath.Base(filename),
		os.O_WRONLY|os.O_CREATE|os.O_EXCL,
		0o600,
	)
	if err != nil {
		return errors.Join(
			fmt.Errorf("create release artifact %q: %w", filename, err),
			root.Close(),
		)
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	rootCloseErr := root.Close()
	if err := errors.Join(writeErr, closeErr, rootCloseErr); err != nil {
		return fmt.Errorf("write release artifact %q: %w", filename, err)
	}
	return nil
}

func readScopedFile(rootPath, name string) ([]byte, error) {
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return nil, fmt.Errorf("open scoped root %q: %w", rootPath, err)
	}
	file, err := root.Open(filepath.ToSlash(name))
	if err != nil {
		return nil, errors.Join(err, root.Close())
	}
	data, readErr := io.ReadAll(file)
	if err := errors.Join(readErr, file.Close(), root.Close()); err != nil {
		return nil, err
	}
	return data, nil
}

func boundedText(data []byte) string {
	if len(data) > maxDiagnosticBytes {
		data = data[len(data)-maxDiagnosticBytes:]
	}
	return strings.TrimSpace(string(data))
}
