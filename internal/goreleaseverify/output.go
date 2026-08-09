package goreleaseverify

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func writeVerifiedOutput(
	configured string,
	artifacts map[string][]byte,
	files []string,
) (_ string, resultErr error) {
	if configured == "" {
		return "", errors.New("verified output directory is required")
	}
	target, resolveErr := filepath.Abs(configured)
	if resolveErr != nil {
		return "", fmt.Errorf("resolve verified output directory: %w", resolveErr)
	}
	name := filepath.Base(target)
	if !safeName(name) {
		return "", fmt.Errorf("verified output directory name %q is unsafe", name)
	}
	parent, parentErr := realDirectory(filepath.Dir(target), "verified output parent")
	if parentErr != nil {
		return "", parentErr
	}
	target = filepath.Join(parent, name)
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return "", fmt.Errorf("verified output directory %q already exists", target)
		}
		return "", fmt.Errorf("inspect verified output directory: %w", err)
	}
	if err := os.Mkdir(target, 0o700); err != nil {
		return "", fmt.Errorf("claim absent verified output directory without replacement: %w", err)
	}
	owned := true
	defer func() {
		if owned {
			resultErr = errors.Join(resultErr, os.RemoveAll(target))
		}
	}()
	root, rootErr := os.OpenRoot(target)
	if rootErr != nil {
		return "", fmt.Errorf("open claimed verified output root: %w", rootErr)
	}
	targetInfo, targetErr := os.Lstat(target)
	rootInfo, rootInfoErr := root.Stat(".")
	if targetErr != nil || rootInfoErr != nil || !targetInfo.IsDir() ||
		targetInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(targetInfo, rootInfo) {
		return "", errors.Join(
			errors.New("claimed verified output directory changed while opening"),
			targetErr,
			rootInfoErr,
			root.Close(),
		)
	}
	for _, fileName := range files {
		if writeErr := writeVerifiedFile(root, fileName, artifacts[fileName]); writeErr != nil {
			return "", errors.Join(writeErr, root.Close())
		}
	}
	if err := root.Close(); err != nil {
		return "", err
	}
	if err := revalidateArtifactSet(target, artifacts, files); err != nil {
		return "", fmt.Errorf("recheck verifier-owned output: %w", err)
	}
	owned = false
	return target, nil
}

func writeVerifiedFile(root *os.Root, name string, content []byte) (resultErr error) {
	if !safeName(name) {
		return fmt.Errorf("verified output filename %q is unsafe", name)
	}
	file, err := root.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("create verified output file %q: %w", name, err)
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	written, err := file.Write(content)
	if err != nil {
		return fmt.Errorf("write verified output file %q: wrote %d of %d bytes: %w", name, written, len(content), err)
	}
	if written != len(content) {
		return fmt.Errorf("write verified output file %q: wrote %d of %d bytes", name, written, len(content))
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync verified output file %q: %w", name, err)
	}
	if err := root.Chmod(name, 0o644); err != nil {
		return fmt.Errorf("set verified output file %q mode: %w", name, err)
	}
	return nil
}

func compareArtifactMaps(actual, expected map[string][]byte, files []string) error {
	for _, name := range files {
		if !bytes.Equal(actual[name], expected[name]) {
			return fmt.Errorf("go module artifact %q changed during verification", name)
		}
	}
	return nil
}
