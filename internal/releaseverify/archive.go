package releaseverify

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxArchiveEntries       = 100_000
	maxArchiveExpandedBytes = 768 << 20
	maxArchiveEntryBytes    = 256 << 20
)

type archiveEntry struct {
	name       string
	mode       os.FileMode
	data       []byte
	linkTarget string
}

func readArchive(
	ctx context.Context,
	data []byte,
	windows bool,
	epoch time.Time,
) ([]archiveEntry, error) {
	if len(data) == 0 || len(data) > maxArtifactBytes {
		return nil, errors.New("archive is empty or exceeds the size limit")
	}
	if windows {
		return readZipArchive(ctx, data, epoch)
	}
	return readTarGzipArchive(ctx, data, epoch)
}

func readZipArchive(
	ctx context.Context,
	data []byte,
	epoch time.Time,
) ([]archiveEntry, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("open ZIP: %w", err)
	}
	if len(reader.File) > maxArchiveEntries {
		return nil, fmt.Errorf("ZIP has more than %d entries", maxArchiveEntries)
	}
	entries := make([]archiveEntry, 0, len(reader.File))
	seen := make(map[string]struct{}, len(reader.File))
	var expanded uint64
	for _, item := range reader.File {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("read ZIP: %w", err)
		}
		if err := validateArchivePath(item.Name); err != nil {
			return nil, err
		}
		if _, duplicate := seen[item.Name]; duplicate {
			return nil, fmt.Errorf("ZIP contains duplicate path %q", item.Name)
		}
		seen[item.Name] = struct{}{}
		if item.Method != zip.Deflate || !item.Modified.Equal(epoch) ||
			!item.Mode().IsRegular() {
			return nil, fmt.Errorf("ZIP entry %q has noncanonical metadata", item.Name)
		}
		if item.UncompressedSize64 > maxArchiveEntryBytes ||
			expanded+item.UncompressedSize64 > maxArchiveExpandedBytes {
			return nil, fmt.Errorf("ZIP entry %q exceeds extraction limits", item.Name)
		}
		expanded += item.UncompressedSize64
		source, err := item.Open()
		if err != nil {
			return nil, fmt.Errorf("open ZIP entry %q: %w", item.Name, err)
		}
		data, readErr := io.ReadAll(io.LimitReader(source, maxArchiveEntryBytes+1))
		closeErr := source.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			return nil, fmt.Errorf("read ZIP entry %q: %w", item.Name, err)
		}
		if len(data) > maxArchiveEntryBytes || uint64(len(data)) != item.UncompressedSize64 {
			return nil, fmt.Errorf("ZIP entry %q has an invalid expanded size", item.Name)
		}
		entries = append(entries, archiveEntry{
			name: item.Name,
			mode: item.Mode().Perm(),
			data: data,
		})
	}
	return entries, nil
}

func readTarGzipArchive(
	ctx context.Context,
	data []byte,
	epoch time.Time,
) ([]archiveEntry, error) {
	buffered := bufio.NewReader(bytes.NewReader(data))
	gzipReader, err := gzip.NewReader(buffered)
	if err != nil {
		return nil, fmt.Errorf("open gzip: %w", err)
	}
	gzipReader.Multistream(false)
	if !gzipReader.ModTime.Equal(epoch) || gzipReader.OS != 255 ||
		gzipReader.Name != "" || gzipReader.Comment != "" {
		return nil, closeGzip(
			gzipReader,
			errors.New("gzip header has noncanonical metadata"),
		)
	}
	tarReader := tar.NewReader(gzipReader)
	entries := make([]archiveEntry, 0)
	seen := make(map[string]struct{})
	var expanded int64
	for {
		if contextErr := ctx.Err(); contextErr != nil {
			return nil, closeGzip(gzipReader, fmt.Errorf("read tar.gz: %w", contextErr))
		}
		header, nextErr := tarReader.Next()
		if errors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return nil, closeGzip(gzipReader, fmt.Errorf("read tar header: %w", nextErr))
		}
		if len(entries) >= maxArchiveEntries {
			return nil, closeGzip(
				gzipReader,
				fmt.Errorf("tar has more than %d entries", maxArchiveEntries),
			)
		}
		if pathErr := validateArchivePath(header.Name); pathErr != nil {
			return nil, closeGzip(gzipReader, pathErr)
		}
		if _, duplicate := seen[header.Name]; duplicate {
			return nil, closeGzip(
				gzipReader,
				fmt.Errorf("tar contains duplicate path %q", header.Name),
			)
		}
		seen[header.Name] = struct{}{}
		if !header.ModTime.Equal(epoch) || !header.AccessTime.Equal(epoch) ||
			!header.ChangeTime.Equal(epoch) || header.Uid != 0 || header.Gid != 0 ||
			header.Uname != "" || header.Gname != "" {
			return nil, closeGzip(
				gzipReader,
				fmt.Errorf("tar entry %q has noncanonical metadata", header.Name),
			)
		}
		if header.Mode < 0 || header.Mode > 0o777 {
			return nil, closeGzip(
				gzipReader,
				fmt.Errorf("tar entry %q has an invalid mode", header.Name),
			)
		}
		entry := archiveEntry{name: header.Name, mode: os.FileMode(header.Mode)}
		switch header.Typeflag {
		case tar.TypeReg:
			if header.Size < 0 || header.Size > maxArchiveEntryBytes ||
				expanded+header.Size > maxArchiveExpandedBytes {
				return nil, closeGzip(
					gzipReader,
					fmt.Errorf("tar entry %q exceeds extraction limits", header.Name),
				)
			}
			expanded += header.Size
			entry.data, err = io.ReadAll(io.LimitReader(tarReader, maxArchiveEntryBytes+1))
			if err != nil {
				return nil, closeGzip(
					gzipReader,
					fmt.Errorf("read tar entry %q: %w", header.Name, err),
				)
			}
			if int64(len(entry.data)) != header.Size {
				return nil, closeGzip(
					gzipReader,
					fmt.Errorf("tar entry %q has an invalid expanded size", header.Name),
				)
			}
		case tar.TypeSymlink:
			if header.Size != 0 || !safeLinkTarget(header.Name, header.Linkname) {
				return nil, closeGzip(
					gzipReader,
					fmt.Errorf("tar symlink %q has unsafe metadata", header.Name),
				)
			}
			entry.linkTarget = header.Linkname
		default:
			return nil, closeGzip(
				gzipReader,
				fmt.Errorf("tar entry %q has unsupported type", header.Name),
			)
		}
		entries = append(entries, entry)
	}
	if err := gzipReader.Close(); err != nil {
		return nil, fmt.Errorf("close gzip: %w", err)
	}
	if _, err := buffered.Peek(1); !errors.Is(err, io.EOF) {
		return nil, errors.New("tar.gz has trailing or concatenated data")
	}
	return entries, nil
}

func closeGzip(reader *gzip.Reader, cause error) error {
	return errors.Join(cause, reader.Close())
}

func validateArchivePath(name string) error {
	if name == "" || !utf8.ValidString(name) || strings.ContainsRune(name, 0) ||
		strings.Contains(name, "\\") || path.IsAbs(name) || path.Clean(name) != name ||
		name == "." || portableVolumeName(name) != "" ||
		!filepath.IsLocal(filepath.FromSlash(name)) {
		return fmt.Errorf("archive path %q is unsafe", name)
	}
	return nil
}

func safeLinkTarget(name, target string) bool {
	if target == "" || !utf8.ValidString(target) || strings.ContainsRune(target, 0) ||
		strings.ContainsAny(target, "\\\r\n") || path.IsAbs(target) ||
		portableVolumeName(target) != "" {
		return false
	}
	resolved := path.Clean(path.Join(path.Dir(name), target))
	return resolved != ".." && !strings.HasPrefix(resolved, "../") &&
		!path.IsAbs(resolved) && portableVolumeName(resolved) == ""
}

func portableVolumeName(value string) string {
	if len(value) >= 2 && value[1] == ':' &&
		(value[0] >= 'a' && value[0] <= 'z' || value[0] >= 'A' && value[0] <= 'Z') {
		return value[:2]
	}
	if strings.HasPrefix(value, "//") || strings.HasPrefix(value, "\\\\") {
		return value[:2]
	}
	return ""
}
