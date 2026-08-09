package goreleaseverify

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const legacyRegularType byte = 0

type isolatedWorkspace struct {
	base        string
	source      string
	moduleCache string
	buildCache  string
	goPath      string
	temporary   string
}

func materializeSourceArchive(
	archive []byte,
	source sourceIdentity,
	policy releasePolicy,
) (_ isolatedWorkspace, resultErr error) {
	base, err := os.MkdirTemp("", "spice-go-release-verify-")
	if err != nil {
		return isolatedWorkspace{}, fmt.Errorf("create isolated verifier workspace: %w", err)
	}
	workspace := isolatedWorkspace{
		base: base, source: filepath.Join(base, "source"),
		moduleCache: filepath.Join(base, "module-cache"), buildCache: filepath.Join(base, "build-cache"),
		goPath: filepath.Join(base, "go-path"), temporary: filepath.Join(base, "temporary"),
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, workspace.Close())
		}
	}()
	for _, directory := range []string{
		workspace.source,
		workspace.moduleCache,
		workspace.buildCache,
		workspace.goPath,
		workspace.temporary,
	} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			return isolatedWorkspace{}, fmt.Errorf("create isolated verifier directory: %w", err)
		}
	}
	if err := extractTrustedArchive(archive, workspace.source, source, policy); err != nil {
		return isolatedWorkspace{}, err
	}
	return workspace, nil
}

func (workspace isolatedWorkspace) Close() error {
	if workspace.base == "" {
		return nil
	}
	return os.RemoveAll(workspace.base)
}

func extractTrustedArchive(
	archive []byte,
	destination string,
	source sourceIdentity,
	policy releasePolicy,
) (resultErr error) {
	gzipReader, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return fmt.Errorf("open verified source archive: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, gzipReader.Close()) }()
	root, err := os.OpenRoot(destination)
	if err != nil {
		return fmt.Errorf("open isolated source root: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	prefix := policy.repository + "_" + strings.TrimPrefix(policy.version, "v") + "/"
	reader := tar.NewReader(gzipReader)
	seen := make(map[string]struct{}, len(source.entries))
	var total int64
	for {
		header, nextErr := reader.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return fmt.Errorf("read verified source archive: %w", nextErr)
		}
		if header.Typeflag == tar.TypeXGlobalHeader && header.Name == "pax_global_header" {
			continue
		}
		if !strings.HasPrefix(header.Name, prefix) {
			return fmt.Errorf("verified source archive path %q is outside canonical root", header.Name)
		}
		relative := strings.TrimSuffix(strings.TrimPrefix(header.Name, prefix), "/")
		if relative == "" {
			if header.Typeflag != tar.TypeDir {
				return errors.New("verified source archive root is not a directory")
			}
			continue
		}
		if err := validatePortablePath(relative); err != nil {
			return fmt.Errorf("verified source archive path %q: %w", relative, err)
		}
		if header.Typeflag == tar.TypeDir {
			if err := root.MkdirAll(filepath.FromSlash(relative), 0o755); err != nil {
				return fmt.Errorf("create isolated source directory %q: %w", relative, err)
			}
			continue
		}
		if header.Typeflag != tar.TypeReg && header.Typeflag != legacyRegularType {
			return fmt.Errorf("verified source archive path %q has unsupported type %d", relative, header.Typeflag)
		}
		entry, found := findEntry(source.entries, relative)
		if !found {
			return fmt.Errorf("verified source archive contains unexpected file %q", relative)
		}
		if _, duplicate := seen[relative]; duplicate {
			return fmt.Errorf("verified source archive repeats file %q", relative)
		}
		mode := os.FileMode(0o644)
		archiveMode := int64(0o664)
		if entry.mode == "100755" {
			mode = 0o755
			archiveMode = 0o775
		}
		if header.Mode&0o777 != archiveMode || header.Size < 0 || header.Size > maxArchiveBytes ||
			total > maxArchiveSource-header.Size {
			return fmt.Errorf("verified source archive file %q has invalid mode or size", relative)
		}
		total += header.Size
		parent := path.Dir(relative)
		if parent != "." {
			if err := root.MkdirAll(filepath.FromSlash(parent), 0o755); err != nil {
				return fmt.Errorf("create isolated source parent for %q: %w", relative, err)
			}
		}
		if err := writeArchiveFile(root, filepath.FromSlash(relative), mode, header.Size, reader); err != nil {
			return fmt.Errorf("materialize verified source file %q: %w", relative, err)
		}
		seen[relative] = struct{}{}
	}
	if len(seen) != len(source.entries) {
		return fmt.Errorf(
			"verified source archive contains %d files; trusted Git tree contains %d",
			len(seen),
			len(source.entries),
		)
	}
	return nil
}

func writeArchiveFile(
	root *os.Root,
	name string,
	mode os.FileMode,
	size int64,
	reader io.Reader,
) (resultErr error) {
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	written, err := io.CopyN(file, reader, size)
	if err != nil {
		return fmt.Errorf("copy %d of %d bytes: %w", written, size, err)
	}
	if written != size {
		return fmt.Errorf("copy %d of %d bytes", written, size)
	}
	if err := file.Sync(); err != nil {
		return err
	}
	return root.Chmod(name, mode)
}
