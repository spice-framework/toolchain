package release

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/spice-framework/toolchain/internal/gitenv"
)

const (
	gitModeRegular    = "100644"
	gitModeExecutable = "100755"
	gitModeSymlink    = "120000"
	gitModeGitlink    = "160000"
)

type snapshotEntryKind uint8

const (
	snapshotRegular snapshotEntryKind = iota + 1
	snapshotSymlink
)

type snapshotEntry struct {
	path       string
	objectID   string
	mode       os.FileMode
	kind       snapshotEntryKind
	data       []byte
	linkTarget string
}

type sourceSnapshot struct {
	entries []snapshotEntry
}

func validateRequiredSnapshotFiles(snapshot sourceSnapshot) error {
	for _, filename := range []string{"go.mod", "LICENSE", "README.md"} {
		if _, err := snapshot.file(filename); err != nil {
			return fmt.Errorf(
				"build release: required source file %q: %w",
				filename,
				err,
			)
		}
	}
	return nil
}

func captureSourceSnapshot(
	ctx context.Context,
	repository string,
	commit string,
) (sourceSnapshot, error) {
	if ctx == nil {
		return sourceSnapshot{}, errors.New("capture release source snapshot: context is nil")
	}
	// #nosec G204 -- commit is an exact validated object ID passed as one Git argument; no shell is involved.
	command := exec.CommandContext(
		ctx,
		"git",
		"ls-tree",
		"-rz",
		"--full-tree",
		commit,
	)
	command.Dir = repository
	command.Env = gitenv.ReadOnly(os.Environ())
	tree, err := command.Output()
	if err != nil {
		return sourceSnapshot{}, fmt.Errorf(
			"enumerate release source at commit %q: %w%s",
			commit,
			err,
			gitCommandStderr(err),
		)
	}
	entries, err := parseSnapshotTree(tree)
	if err != nil {
		return sourceSnapshot{}, err
	}
	if err := readSnapshotBlobs(ctx, repository, entries); err != nil {
		return sourceSnapshot{}, err
	}
	if err := validateSnapshotEntries(entries); err != nil {
		return sourceSnapshot{}, err
	}
	return sourceSnapshot{entries: entries}, nil
}

func resolveSnapshotCommit(
	ctx context.Context,
	repository string,
	reference string,
) (string, error) {
	if reference == "" {
		reference = "HEAD"
	}
	// #nosec G204 -- reference is passed as one Git argument; no shell is involved, and the result must be an exact object ID.
	command := exec.CommandContext(
		ctx,
		"git",
		"rev-parse",
		"--verify",
		reference+"^{commit}",
	)
	command.Dir = repository
	command.Env = gitenv.ReadOnly(os.Environ())
	output, err := command.Output()
	if err != nil {
		return "", fmt.Errorf(
			"resolve release source commit %q: %w%s",
			reference,
			err,
			gitCommandStderr(err),
		)
	}
	commit := strings.TrimSpace(string(output))
	if len(commit) != 40 && len(commit) != 64 {
		return "", fmt.Errorf(
			"resolve release source commit %q: unexpected object ID %q",
			reference,
			commit,
		)
	}
	if _, err := hex.DecodeString(commit); err != nil {
		return "", fmt.Errorf(
			"resolve release source commit %q: invalid object ID: %w",
			reference,
			err,
		)
	}
	return commit, nil
}

func parseSnapshotTree(tree []byte) ([]snapshotEntry, error) {
	records := bytes.Split(tree, []byte{0})
	entries := make([]snapshotEntry, 0, len(records))
	for index, record := range records {
		if len(record) == 0 {
			if index != len(records)-1 {
				return nil, fmt.Errorf("release source tree entry %d is empty", index)
			}
			continue
		}
		metadata, filename, found := bytes.Cut(record, []byte{'\t'})
		if !found {
			return nil, fmt.Errorf("release source tree entry %d has no path separator", index)
		}
		fields := bytes.Fields(metadata)
		if len(fields) != 3 {
			return nil, fmt.Errorf("release source tree entry %d has invalid metadata", index)
		}
		entry, err := snapshotEntryFromTree(
			string(fields[0]),
			string(fields[1]),
			string(fields[2]),
			string(filename),
		)
		if err != nil {
			return nil, fmt.Errorf("release source tree entry %d: %w", index, err)
		}
		entries = append(entries, entry)
	}
	slices.SortFunc(entries, func(left, right snapshotEntry) int {
		return strings.Compare(left.path, right.path)
	})
	for index := 1; index < len(entries); index++ {
		if entries[index-1].path == entries[index].path {
			return nil, fmt.Errorf(
				"release source tree contains duplicate path %q",
				entries[index].path,
			)
		}
	}
	portablePaths := make(map[string]string, len(entries))
	for _, entry := range entries {
		key := strings.ToLower(entry.path)
		if existing, found := portablePaths[key]; found && existing != entry.path {
			return nil, fmt.Errorf(
				"release source paths %q and %q collide on case-insensitive filesystems",
				existing,
				entry.path,
			)
		}
		portablePaths[key] = entry.path
	}
	return entries, nil
}

func snapshotEntryFromTree(
	mode string,
	objectType string,
	objectID string,
	filename string,
) (snapshotEntry, error) {
	if err := validateSnapshotPath(filename); err != nil {
		return snapshotEntry{}, err
	}
	if err := validateGitObjectID(objectID); err != nil {
		return snapshotEntry{}, fmt.Errorf("path %q: %w", filename, err)
	}
	entry := snapshotEntry{path: filename, objectID: objectID}
	switch mode {
	case gitModeRegular:
		entry.kind = snapshotRegular
		entry.mode = 0o644
	case gitModeExecutable:
		entry.kind = snapshotRegular
		entry.mode = 0o755
	case gitModeSymlink:
		entry.kind = snapshotSymlink
		entry.mode = 0o777
	case gitModeGitlink:
		return snapshotEntry{}, fmt.Errorf("path %q is a gitlink, which release snapshots forbid", filename)
	default:
		return snapshotEntry{}, fmt.Errorf(
			"path %q uses unsupported Git mode %q",
			filename,
			mode,
		)
	}
	if objectType != "blob" {
		return snapshotEntry{}, fmt.Errorf(
			"path %q with mode %s has unsupported Git object type %q",
			filename,
			mode,
			objectType,
		)
	}
	return entry, nil
}

func validateSnapshotPath(filename string) error {
	if filename == "" || !utf8.ValidString(filename) ||
		strings.ContainsRune(filename, 0) ||
		strings.Contains(filename, "\\") ||
		path.IsAbs(filename) ||
		path.Clean(filename) != filename ||
		filename == "." ||
		filename == ".git" || strings.HasPrefix(filename, ".git/") ||
		portableVolumeName(filename) != "" ||
		!filepath.IsLocal(filepath.FromSlash(filename)) {
		return fmt.Errorf("release source path %q is unsafe", filename)
	}
	return nil
}

func validateGitObjectID(objectID string) error {
	if len(objectID) != 40 && len(objectID) != 64 {
		return fmt.Errorf("git object ID %q has an unsupported length", objectID)
	}
	if _, err := hex.DecodeString(objectID); err != nil {
		return fmt.Errorf("git object ID %q is invalid: %w", objectID, err)
	}
	return nil
}

func readSnapshotBlobs(
	ctx context.Context,
	repository string,
	entries []snapshotEntry,
) error {
	if len(entries) == 0 {
		return errors.New("release source tree at HEAD is empty")
	}
	var requests strings.Builder
	for _, entry := range entries {
		requests.WriteString(entry.objectID)
		requests.WriteByte('\n')
	}
	command := exec.CommandContext(ctx, "git", "cat-file", "--batch")
	command.Dir = repository
	command.Env = append(gitenv.ReadOnly(os.Environ()), "GIT_NO_LAZY_FETCH=1")
	command.Stdin = strings.NewReader(requests.String())
	stdout, err := command.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open release Git blob stream: %w", err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return fmt.Errorf("start release Git blob stream: %w", err)
	}
	reader := bufio.NewReader(stdout)
	for index := range entries {
		data, readErr := readSnapshotBlob(reader, entries[index].objectID)
		if readErr != nil {
			return stopSnapshotBlobCommand(command, &stderr, readErr)
		}
		if entries[index].kind == snapshotSymlink {
			target := string(data)
			if targetErr := validateSnapshotSymlink(entries[index].path, target); targetErr != nil {
				return stopSnapshotBlobCommand(command, &stderr, targetErr)
			}
			entries[index].linkTarget = target
			continue
		}
		entries[index].data = data
	}
	if err := command.Wait(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("read release Git blobs: %w", ctxErr)
		}
		return fmt.Errorf(
			"read release Git blobs: %w%s",
			err,
			renderGitStderr(stderr.Bytes()),
		)
	}
	return nil
}

func readSnapshotBlob(reader *bufio.Reader, objectID string) ([]byte, error) {
	header, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("read Git blob %s header: %w", objectID, err)
	}
	fields := strings.Fields(strings.TrimSuffix(header, "\n"))
	if len(fields) != 3 || fields[0] != objectID || fields[1] != "blob" {
		return nil, fmt.Errorf(
			"git blob %s returned invalid batch header %q",
			objectID,
			strings.TrimSpace(header),
		)
	}
	size, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil || size < 0 || size > int64(^uint(0)>>1) {
		return nil, fmt.Errorf("git blob %s returned invalid size %q", objectID, fields[2])
	}
	data := make([]byte, int(size))
	if _, readErr := io.ReadFull(reader, data); readErr != nil {
		return nil, fmt.Errorf("read Git blob %s content: %w", objectID, readErr)
	}
	terminator, err := reader.ReadByte()
	if err != nil || terminator != '\n' {
		return nil, fmt.Errorf("git blob %s has no batch terminator", objectID)
	}
	return data, nil
}

func stopSnapshotBlobCommand(
	command *exec.Cmd,
	stderr *bytes.Buffer,
	cause error,
) error {
	killErr := command.Process.Kill()
	if errors.Is(killErr, os.ErrProcessDone) {
		killErr = nil
	}
	waitErr := command.Wait()
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		waitErr = nil
	}
	result := errors.Join(cause, killErr, waitErr)
	if detail := strings.TrimPrefix(renderGitStderr(stderr.Bytes()), ": "); detail != "" {
		result = errors.Join(result, errors.New(detail))
	}
	return result
}

func validateSnapshotSymlink(filename, target string) error {
	if target == "" || !utf8.ValidString(target) ||
		strings.ContainsRune(target, 0) ||
		strings.ContainsAny(target, "\\\r\n") ||
		path.IsAbs(target) || portableVolumeName(target) != "" {
		return fmt.Errorf(
			"release source symlink %q has unsafe target %q",
			filename,
			target,
		)
	}
	resolved := path.Clean(path.Join(path.Dir(filename), target))
	if resolved == ".." || strings.HasPrefix(resolved, "../") ||
		path.IsAbs(resolved) || portableVolumeName(resolved) != "" {
		return fmt.Errorf(
			"release source symlink %q escapes the source root through target %q",
			filename,
			target,
		)
	}
	return nil
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

func validateSnapshotEntries(entries []snapshotEntry) error {
	for _, entry := range entries {
		if entry.kind != snapshotSymlink {
			continue
		}
		prefix := entry.path + "/"
		for _, candidate := range entries {
			if strings.HasPrefix(candidate.path, prefix) {
				return fmt.Errorf(
					"release source symlink %q is also a directory prefix",
					entry.path,
				)
			}
		}
	}
	return nil
}

func (snapshot sourceSnapshot) file(filename string) ([]byte, error) {
	index, found := slices.BinarySearchFunc(
		snapshot.entries,
		filename,
		func(entry snapshotEntry, name string) int {
			return strings.Compare(entry.path, name)
		},
	)
	if !found || snapshot.entries[index].kind != snapshotRegular {
		return nil, fmt.Errorf("release source snapshot has no regular file %q", filename)
	}
	return slices.Clone(snapshot.entries[index].data), nil
}

func (snapshot sourceSnapshot) materialize(rootPath string) (resultErr error) {
	// #nosec G302 -- this is a private directory; owner search permission is required to materialize and build the snapshot.
	if err := os.Chmod(rootPath, 0o700); err != nil {
		return fmt.Errorf("make release build root private: %w", err)
	}
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		return fmt.Errorf("open release build root: %w", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, root.Close())
	}()
	for _, entry := range snapshot.entries {
		directory := path.Dir(entry.path)
		if directory == "." {
			continue
		}
		if err := root.MkdirAll(directory, 0o700); err != nil {
			return fmt.Errorf("create release source directory %q: %w", directory, err)
		}
	}
	for _, entry := range snapshot.entries {
		if entry.kind != snapshotRegular {
			continue
		}
		file, err := root.OpenFile(
			entry.path,
			os.O_WRONLY|os.O_CREATE|os.O_EXCL,
			entry.mode,
		)
		if err != nil {
			return fmt.Errorf("create release source file %q: %w", entry.path, err)
		}
		_, writeErr := file.Write(entry.data)
		closeErr := file.Close()
		chmodErr := root.Chmod(entry.path, entry.mode)
		if err := errors.Join(writeErr, closeErr, chmodErr); err != nil {
			return fmt.Errorf("materialize release source file %q: %w", entry.path, err)
		}
	}
	for _, entry := range snapshot.entries {
		if entry.kind != snapshotSymlink {
			continue
		}
		if err := root.Symlink(entry.linkTarget, entry.path); err != nil {
			return fmt.Errorf(
				"materialize release source symlink %q -> %q: %w",
				entry.path,
				entry.linkTarget,
				err,
			)
		}
	}
	return nil
}

func (snapshot sourceSnapshot) archiveEntries(base string) []archiveEntry {
	entries := make([]archiveEntry, len(snapshot.entries))
	for index, entry := range snapshot.entries {
		entries[index] = archiveEntry{
			name:       path.Join(base, entry.path),
			mode:       entry.mode,
			data:       slices.Clone(entry.data),
			linkTarget: entry.linkTarget,
		}
	}
	return entries
}

func gitCommandStderr(err error) string {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return ""
	}
	return renderGitStderr(exitErr.Stderr)
}

func renderGitStderr(stderr []byte) string {
	detail := boundedText(stderr)
	if detail == "" {
		return ""
	}
	return ": " + detail
}
