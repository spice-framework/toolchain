package releaseverify

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
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spice-framework/toolchain/internal/gitenv"
)

const maxGitDiagnosticBytes = 32 << 10

type sourceEntry struct {
	path       string
	objectID   string
	mode       os.FileMode
	data       []byte
	linkTarget string
}

func trustedRegularFile(entries []sourceEntry, name string) ([]byte, error) {
	index, found := slices.BinarySearchFunc(
		entries,
		name,
		func(entry sourceEntry, filename string) int {
			return strings.Compare(entry.path, filename)
		},
	)
	if !found || entries[index].linkTarget != "" {
		return nil, fmt.Errorf("trusted source has no regular file %q", name)
	}
	return slices.Clone(entries[index].data), nil
}

func validateExactCommit(commit string) error {
	if len(commit) != 40 && len(commit) != 64 {
		return fmt.Errorf("verify release: commit must be an exact 40- or 64-hex object ID")
	}
	if _, err := hex.DecodeString(commit); err != nil {
		return fmt.Errorf("verify release: commit is not hexadecimal: %w", err)
	}
	return nil
}

func trustedSource(
	ctx context.Context,
	repository string,
	commit string,
) (time.Time, []sourceEntry, error) {
	resolved, err := gitOutput(ctx, repository, "rev-parse", "--verify", commit+"^{commit}")
	if err != nil {
		return time.Time{}, nil, fmt.Errorf("resolve trusted commit: %w", err)
	}
	if !strings.EqualFold(strings.TrimSpace(string(resolved)), commit) {
		return time.Time{}, nil, errors.New("trusted commit did not resolve to the exact requested object")
	}
	epochText, err := gitOutput(ctx, repository, "show", "-s", "--format=%ct", commit)
	if err != nil {
		return time.Time{}, nil, fmt.Errorf("read trusted commit epoch: %w", err)
	}
	seconds, err := strconv.ParseInt(strings.TrimSpace(string(epochText)), 10, 64)
	if err != nil {
		return time.Time{}, nil, fmt.Errorf("parse trusted commit epoch: %w", err)
	}
	tree, err := gitOutput(ctx, repository, "ls-tree", "-rz", "--full-tree", commit)
	if err != nil {
		return time.Time{}, nil, fmt.Errorf("read trusted commit tree: %w", err)
	}
	entries, err := parseGitTree(tree)
	if err != nil {
		return time.Time{}, nil, err
	}
	if err := readGitBlobs(ctx, repository, entries); err != nil {
		return time.Time{}, nil, err
	}
	return time.Unix(seconds, 0).UTC(), entries, nil
}

func parseGitTree(data []byte) ([]sourceEntry, error) {
	records := bytes.Split(data, []byte{0})
	entries := make([]sourceEntry, 0, len(records))
	portable := make(map[string]string)
	for index, record := range records {
		if len(record) == 0 {
			if index != len(records)-1 {
				return nil, fmt.Errorf("trusted tree entry %d is empty", index)
			}
			continue
		}
		metadata, name, found := bytes.Cut(record, []byte{'\t'})
		fields := bytes.Fields(metadata)
		if !found || len(fields) != 3 || string(fields[1]) != "blob" {
			return nil, fmt.Errorf("trusted tree entry %d has unsupported metadata", index)
		}
		filename := string(name)
		if err := validateArchivePath(filename); err != nil {
			return nil, fmt.Errorf("trusted tree: %w", err)
		}
		entry := sourceEntry{path: filename, objectID: string(fields[2])}
		switch string(fields[0]) {
		case "100644":
			entry.mode = 0o644
		case "100755":
			entry.mode = 0o755
		case "120000":
			entry.mode = 0o777
		default:
			return nil, fmt.Errorf(
				"trusted tree path %q has unsupported mode %q",
				filename,
				fields[0],
			)
		}
		if err := validateGitObjectID(entry.objectID); err != nil {
			return nil, fmt.Errorf("trusted tree path %q: %w", filename, err)
		}
		key := strings.ToLower(filename)
		if prior, found := portable[key]; found {
			return nil, fmt.Errorf("trusted tree paths %q and %q collide", prior, filename)
		}
		portable[key] = filename
		entries = append(entries, entry)
	}
	if len(entries) == 0 || len(entries) > maxArchiveEntries {
		return nil, errors.New("trusted source tree is empty or exceeds entry limits")
	}
	slices.SortFunc(entries, func(left, right sourceEntry) int {
		return strings.Compare(left.path, right.path)
	})
	return entries, nil
}

func validateGitObjectID(objectID string) error {
	if len(objectID) != 40 && len(objectID) != 64 {
		return fmt.Errorf("git object ID %q has unsupported length", objectID)
	}
	if _, err := hex.DecodeString(objectID); err != nil {
		return fmt.Errorf("git object ID %q is invalid: %w", objectID, err)
	}
	return nil
}

func readGitBlobs(ctx context.Context, repository string, entries []sourceEntry) error {
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
		return fmt.Errorf("open trusted Git blob stream: %w", err)
	}
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return fmt.Errorf("start trusted Git blob stream: %w", err)
	}
	reader := bufio.NewReader(stdout)
	var total int64
	for index := range entries {
		data, readErr := readGitBlob(reader, entries[index].objectID)
		if readErr != nil {
			return stopGitCommand(command, readErr)
		}
		total += int64(len(data))
		if total > maxArchiveExpandedBytes {
			return stopGitCommand(
				command,
				errors.New("trusted source tree exceeds expanded-size limit"),
			)
		}
		if entries[index].mode == 0o777 {
			entries[index].linkTarget = string(data)
			if !safeLinkTarget(entries[index].path, entries[index].linkTarget) {
				return stopGitCommand(
					command,
					fmt.Errorf("trusted symlink %q has unsafe target", entries[index].path),
				)
			}
		} else {
			entries[index].data = data
		}
	}
	if err := command.Wait(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("read trusted Git blobs: %w", ctxErr)
		}
		return fmt.Errorf("read trusted Git blobs: %w: %s", err, bounded(stderr.Bytes()))
	}
	return nil
}

func readGitBlob(reader *bufio.Reader, objectID string) ([]byte, error) {
	header, err := reader.ReadString('\n')
	if err != nil {
		return nil, fmt.Errorf("read Git blob %s header: %w", objectID, err)
	}
	fields := strings.Fields(header)
	if len(fields) != 3 || fields[0] != objectID || fields[1] != "blob" {
		return nil, fmt.Errorf("git blob %s returned invalid metadata", objectID)
	}
	size, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil || size < 0 || size > maxArchiveEntryBytes {
		return nil, fmt.Errorf("git blob %s exceeds entry-size limit", objectID)
	}
	data := make([]byte, int(size))
	if _, readErr := io.ReadFull(reader, data); readErr != nil {
		return nil, fmt.Errorf("read git blob %s: %w", objectID, readErr)
	}
	terminator, err := reader.ReadByte()
	if err != nil || terminator != '\n' {
		return nil, fmt.Errorf("git blob %s has no batch terminator", objectID)
	}
	return data, nil
}

func stopGitCommand(command *exec.Cmd, cause error) error {
	killErr := command.Process.Kill()
	if errors.Is(killErr, os.ErrProcessDone) {
		killErr = nil
	}
	waitErr := command.Wait()
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		waitErr = nil
	}
	return errors.Join(cause, killErr, waitErr)
}

func verifySourceArchive(
	actual []archiveEntry,
	base string,
	expected []sourceEntry,
) (map[string][]byte, error) {
	if len(actual) != len(expected) {
		return nil, fmt.Errorf("source archive has %d entries, require %d", len(actual), len(expected))
	}
	files := make(map[string][]byte, len(actual))
	for index, want := range expected {
		got := actual[index]
		name := path.Join(base, want.path)
		if got.name != name || got.mode != want.mode ||
			got.linkTarget != want.linkTarget || !bytes.Equal(got.data, want.data) {
			return nil, fmt.Errorf("source archive entry %d does not match trusted path %q", index, want.path)
		}
		if want.linkTarget == "" {
			files[want.path] = append([]byte(nil), want.data...)
		}
	}
	for _, required := range []string{"go.mod", "LICENSE", "README.md", "vendor/modules.txt"} {
		if _, found := files[required]; !found {
			return nil, fmt.Errorf("source archive is missing required file %q", required)
		}
	}
	return files, nil
}

func gitOutput(ctx context.Context, repository string, arguments ...string) ([]byte, error) {
	if len(arguments) == 0 {
		return nil, errors.New("run trusted Git command: no arguments")
	}
	// #nosec G204 -- arguments are fixed except for an exact validated object ID.
	command := exec.CommandContext(ctx, "git", arguments...)
	command.Dir = repository
	command.Env = append(gitenv.ReadOnly(os.Environ()), "GIT_NO_LAZY_FETCH=1")
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", arguments[0], err, bounded(stderr.Bytes()))
	}
	return output, nil
}

func bounded(data []byte) string {
	if len(data) > maxGitDiagnosticBytes {
		data = data[len(data)-maxGitDiagnosticBytes:]
	}
	return strings.TrimSpace(string(data))
}
