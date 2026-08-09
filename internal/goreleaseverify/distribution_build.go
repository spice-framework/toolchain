package goreleaseverify

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"
)

type distributionArchiveEntry struct {
	name       string
	content    []byte
	executable bool
}

func buildDistributionTarget(
	ctx context.Context,
	workspace isolatedWorkspace,
	source sourceIdentity,
	policy distributionPolicy,
	target distributionTarget,
	runner distributionGoRunner,
) (_ map[string][]byte, resultErr error) {
	directory, err := os.MkdirTemp(workspace.base, "distribution-build-"+target.goos+"-"+target.goarch+"-")
	if err != nil {
		return nil, fmt.Errorf("create independent distribution build directory: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, os.RemoveAll(directory)) }()
	result := make(map[string][]byte, len(policy.binaries))
	for _, binary := range policy.binaries {
		archiveName := binary.name
		if target.goos == "windows" {
			archiveName += ".exe"
		}
		output := filepath.Join(directory, archiveName)
		linkerFlags := "-buildid= -X=" + policy.versionSymbol + "=" + strings.TrimPrefix(policy.version, "v") +
			" -X=" + policy.commitSymbol + "=" + source.commit
		arguments := []string{
			"build", "-mod=vendor", "-trimpath", "-buildvcs=false", "-ldflags=" + linkerFlags,
			"-o", output, binary.packagePath,
		}
		if _, err := runner.Output(
			ctx, workspace.source, distributionBuildEnvironment(os.Environ(), workspace, target), arguments...,
		); err != nil {
			return nil, fmt.Errorf("independently build %s for %s/%s: %w", binary.packagePath, target.goos, target.goarch, err)
		}
		if err := verifyDistributionBinaryIdentity(ctx, workspace, source, policy, target, binary, output, runner); err != nil {
			return nil, err
		}
		content, err := readAbsoluteRegularFile(output, maxDistributionArtifact)
		if err != nil {
			return nil, fmt.Errorf("read independently built binary %q: %w", binary.name, err)
		}
		if len(content) == 0 {
			return nil, fmt.Errorf("independently built binary %q is empty", binary.name)
		}
		result[archiveName] = content
	}
	return result, nil
}

func distributionBuildEnvironment(
	ambient []string,
	workspace isolatedWorkspace,
	target distributionTarget,
) []string {
	result := trustedGoEnvironment(ambient, workspace, false)
	result = replaceEnvironment(result, "GOFLAGS", "")
	result = replaceEnvironment(result, "GOOS", target.goos)
	result = replaceEnvironment(result, "GOARCH", target.goarch)
	result = replaceEnvironment(result, "GOEXPERIMENT", "")
	result = replaceEnvironment(result, "GOVCS", "off")
	switch target.goarch {
	case "amd64":
		result = replaceEnvironment(result, "GOAMD64", "v1")
	case "arm64":
		result = replaceEnvironment(result, "GOARM64", "v8.0")
	}
	return result
}

func replaceEnvironment(environment []string, key, value string) []string {
	prefix := strings.ToUpper(key) + "="
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if !strings.HasPrefix(strings.ToUpper(entry), prefix) {
			result = append(result, entry)
		}
	}
	return append(result, key+"="+value)
}

func verifyDistributionBinaryIdentity(
	ctx context.Context,
	workspace isolatedWorkspace,
	source sourceIdentity,
	policy distributionPolicy,
	target distributionTarget,
	binary distributionBinary,
	output string,
	runner distributionGoRunner,
) error {
	nm, err := runner.Output(
		ctx,
		workspace.source,
		distributionBuildEnvironment(os.Environ(), workspace, target),
		"tool",
		"nm",
		output,
	)
	if err != nil {
		return fmt.Errorf("inspect independently built %s identity: %w", binary.name, err)
	}
	wanted := map[string]int{policy.versionSymbol: 0, policy.commitSymbol: 0}
	for line := range strings.SplitSeq(string(nm), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		name := fields[len(fields)-1]
		if _, found := wanted[name]; found {
			if fields[len(fields)-2] != "D" {
				return fmt.Errorf("built binary %q identity symbol %s has nm type %s, require D", binary.name, name, fields[len(fields)-2])
			}
			wanted[name]++
		}
	}
	for _, symbol := range []string{policy.versionSymbol, policy.commitSymbol} {
		if wanted[symbol] != 1 {
			return fmt.Errorf("built binary %q has %d exact identity symbols named %s, require one", binary.name, wanted[symbol], symbol)
		}
	}
	if target.goos != runtime.GOOS || target.goarch != runtime.GOARCH {
		return nil
	}
	stdout, stderr, err := runner.Execute(
		ctx, workspace.source, distributionBuildEnvironment(os.Environ(), workspace, target), output, "--version",
	)
	if err != nil {
		return fmt.Errorf("execute independently built %s --version: %w: %s", binary.name, err, stderr)
	}
	want := binary.name + " " + strings.TrimPrefix(policy.version, "v") + " (" + source.commit + ")\n"
	if string(stdout) != want || len(stderr) != 0 {
		return fmt.Errorf("execute %s --version returned stdout %q and stderr %q; require %q", binary.name, stdout, stderr, want)
	}
	return nil
}

type distributionGoRunner interface {
	goRunner
	Execute(context.Context, string, []string, string, ...string) ([]byte, []byte, error)
}

func (runner systemGoRunner) Execute(
	ctx context.Context,
	root string,
	environment []string,
	executable string,
	arguments ...string,
) ([]byte, []byte, error) {
	// #nosec G204 -- executable is a verifier-created absolute path and
	// arguments are fixed by the independent distribution policy.
	command := exec.CommandContext(ctx, executable, arguments...)
	command.Dir = root
	command.Env = environment
	var stdout, stderr boundedBuffer
	stdout.maximum = maxDiagnostic
	stderr.maximum = maxDiagnostic
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, nil, ctxErr
		}
		return stdout.Bytes(), stderr.Bytes(), err
	}
	if stdout.truncated || stderr.truncated {
		return nil, nil, errors.New("distribution binary output exceeds limits")
	}
	return slices.Clone(stdout.Bytes()), slices.Clone(stderr.Bytes()), nil
}

func readAbsoluteRegularFile(name string, maximum int64) (result []byte, resultErr error) {
	if !filepath.IsAbs(name) || !safeName(filepath.Base(name)) {
		return nil, fmt.Errorf("file %q is not an absolute safe verifier output", name)
	}
	parent, err := realDirectory(filepath.Dir(name), "distribution binary parent")
	if err != nil {
		return nil, err
	}
	root, err := os.OpenRoot(parent)
	if err != nil {
		return nil, fmt.Errorf("open distribution binary parent: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	base := filepath.Base(name)
	info, err := root.Lstat(base)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() < 0 || info.Size() > maximum {
		return nil, fmt.Errorf("file %q is not a regular file bounded to %d bytes", name, maximum)
	}
	file, err := root.Open(base)
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.Join(errors.New("file changed while opening"), err)
	}
	content, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(content)) > maximum {
		return nil, fmt.Errorf("file exceeds %d bytes", maximum)
	}
	return content, nil
}

func buildDistributionArchive(
	policy distributionPolicy,
	epoch time.Time,
	target distributionTarget,
	binaries map[string][]byte,
	payloads map[string][]byte,
) ([]byte, error) {
	entries := make([]distributionArchiveEntry, 0, len(binaries)+len(payloads))
	for name, content := range binaries {
		entries = append(entries, distributionArchiveEntry{name: name, content: content, executable: true})
	}
	for name, content := range payloads {
		entries = append(entries, distributionArchiveEntry{name: name, content: content})
	}
	slices.SortFunc(entries, func(left, right distributionArchiveEntry) int {
		return strings.Compare(left.name, right.name)
	})
	prefix := distributionTargetBase(policy, target) + "/"
	if target.goos == "windows" {
		return buildDistributionZip(prefix, epoch.UTC(), entries)
	}
	return buildDistributionTarGzip(prefix, epoch.UTC(), entries)
}

func buildDistributionTarGzip(
	prefix string,
	epoch time.Time,
	entries []distributionArchiveEntry,
) ([]byte, error) {
	output := distributionArchiveBuffer{maximum: maxDistributionArtifact}
	gzipWriter, err := gzip.NewWriterLevel(&output, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	gzipWriter.ModTime = epoch
	gzipWriter.OS = 255
	tarWriter := tar.NewWriter(gzipWriter)
	for _, entry := range entries {
		mode := int64(0o644)
		if entry.executable {
			mode = 0o755
		}
		header := &tar.Header{
			Name: prefix + entry.name, Mode: mode, Size: int64(len(entry.content)),
			ModTime: epoch, AccessTime: epoch, ChangeTime: epoch,
			Typeflag: tar.TypeReg, Format: tar.FormatPAX,
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			return nil, errors.Join(err, tarWriter.Close(), gzipWriter.Close())
		}
		if _, err := tarWriter.Write(entry.content); err != nil {
			return nil, errors.Join(err, tarWriter.Close(), gzipWriter.Close())
		}
	}
	if err := tarWriter.Close(); err != nil {
		return nil, errors.Join(err, gzipWriter.Close())
	}
	if err := gzipWriter.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func buildDistributionZip(
	prefix string,
	epoch time.Time,
	entries []distributionArchiveEntry,
) ([]byte, error) {
	output := distributionArchiveBuffer{maximum: maxDistributionArtifact}
	writer := zip.NewWriter(&output)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: prefix + entry.name, Method: zip.Deflate}
		header.Modified = epoch
		mode := fs.FileMode(0o644)
		if entry.executable {
			mode = 0o755
		}
		header.SetMode(mode)
		file, err := writer.CreateHeader(header)
		if err != nil {
			return nil, errors.Join(err, writer.Close())
		}
		if _, err := file.Write(entry.content); err != nil {
			return nil, errors.Join(err, writer.Close())
		}
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

type distributionArchiveBuffer struct {
	content bytes.Buffer
	maximum int
}

func (buffer *distributionArchiveBuffer) Write(content []byte) (int, error) {
	remaining := buffer.maximum - buffer.content.Len()
	if remaining <= 0 {
		return 0, fmt.Errorf("distribution archive exceeds %d bytes", buffer.maximum)
	}
	if len(content) > remaining {
		written, _ := buffer.content.Write(content[:remaining])
		return written, fmt.Errorf("distribution archive exceeds %d bytes", buffer.maximum)
	}
	return buffer.content.Write(content)
}

func (buffer *distributionArchiveBuffer) Bytes() []byte {
	return bytes.Clone(buffer.content.Bytes())
}
