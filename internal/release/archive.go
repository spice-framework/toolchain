package release

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"time"
)

type archiveEntry struct {
	name string
	mode os.FileMode
	data []byte
}

func writeArchive(
	filename string,
	target Target,
	epoch time.Time,
	entries []archiveEntry,
) error {
	root, err := os.OpenRoot(filepath.Dir(filename))
	if err != nil {
		return fmt.Errorf("open release archive root: %w", err)
	}
	file, err := root.OpenFile(
		filepath.Base(filename),
		os.O_WRONLY|os.O_CREATE|os.O_EXCL,
		0o600,
	)
	if err != nil {
		return errors.Join(
			fmt.Errorf("create release archive %q: %w", filename, err),
			root.Close(),
		)
	}
	var writeErr error
	if target.GOOS == "windows" {
		writeErr = writeZip(file, epoch, entries)
	} else {
		writeErr = writeTarGzip(file, epoch, entries)
	}
	if closeErr := file.Close(); closeErr != nil {
		writeErr = errors.Join(writeErr, fmt.Errorf(
			"close release archive %q: %w",
			filename,
			closeErr,
		))
	}
	return errors.Join(writeErr, root.Close())
}

func writeZip(
	output io.Writer,
	epoch time.Time,
	entries []archiveEntry,
) error {
	writer := zip.NewWriter(output)
	for _, entry := range entries {
		header := &zip.FileHeader{
			Name:     path.Clean(entry.name),
			Method:   zip.Deflate,
			Modified: epoch,
		}
		header.SetMode(entry.mode)
		target, err := writer.CreateHeader(header)
		if err != nil {
			closeErr := writer.Close()
			return errors.Join(
				fmt.Errorf("create ZIP entry %q: %w", entry.name, err),
				closeErr,
			)
		}
		if _, err := target.Write(entry.data); err != nil {
			closeErr := writer.Close()
			return errors.Join(
				fmt.Errorf("write ZIP entry %q: %w", entry.name, err),
				closeErr,
			)
		}
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("close ZIP archive: %w", err)
	}
	return nil
}

func writeTarGzip(
	output io.Writer,
	epoch time.Time,
	entries []archiveEntry,
) error {
	gzipWriter, err := gzip.NewWriterLevel(output, gzip.BestCompression)
	if err != nil {
		return fmt.Errorf("construct gzip writer: %w", err)
	}
	gzipWriter.ModTime = epoch
	gzipWriter.OS = 255
	tarWriter := tar.NewWriter(gzipWriter)
	for _, entry := range entries {
		header := &tar.Header{
			Name:       path.Clean(entry.name),
			Mode:       int64(entry.mode),
			Size:       int64(len(entry.data)),
			ModTime:    epoch,
			AccessTime: epoch,
			ChangeTime: epoch,
			Typeflag:   tar.TypeReg,
			Format:     tar.FormatPAX,
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			return errors.Join(
				fmt.Errorf("create tar entry %q: %w", entry.name, err),
				tarWriter.Close(),
				gzipWriter.Close(),
			)
		}
		if _, err := tarWriter.Write(entry.data); err != nil {
			return errors.Join(
				fmt.Errorf("write tar entry %q: %w", entry.name, err),
				tarWriter.Close(),
				gzipWriter.Close(),
			)
		}
	}
	if err := tarWriter.Close(); err != nil {
		return errors.Join(
			fmt.Errorf("close tar archive: %w", err),
			gzipWriter.Close(),
		)
	}
	if err := gzipWriter.Close(); err != nil {
		return fmt.Errorf("close gzip archive: %w", err)
	}
	return nil
}
