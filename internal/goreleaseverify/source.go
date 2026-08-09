package goreleaseverify

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spice-framework/toolchain/internal/gitenv"
)

type sourceIdentity struct {
	root    string
	commit  string
	epoch   time.Time
	source  string
	entries []gitEntry
}

type gitEntry struct {
	name     string
	objectID string
	mode     string
}

func trustedSource(ctx context.Context, config Config, policy releasePolicy) (sourceIdentity, error) {
	root, err := realDirectory(config.Repository, "trusted repository")
	if err != nil {
		return sourceIdentity{}, err
	}
	if validationErr := validateExactCommit(config.Commit); validationErr != nil {
		return sourceIdentity{}, validationErr
	}
	resolved, err := gitOutput(ctx, root, 128, "rev-parse", "--verify", config.Commit+"^{commit}")
	if err != nil || strings.TrimSpace(string(resolved)) != config.Commit {
		return sourceIdentity{}, errors.New("trusted Go module commit does not resolve to the exact requested object")
	}
	head, err := gitOutput(ctx, root, 128, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || strings.TrimSpace(string(head)) != config.Commit {
		return sourceIdentity{}, errors.New("trusted Go module checkout HEAD does not match the requested commit")
	}
	tag, err := gitOutput(ctx, root, 128, "rev-parse", "--verify", "refs/tags/"+config.Version+"^{commit}")
	if err != nil || strings.TrimSpace(string(tag)) != config.Commit {
		return sourceIdentity{}, fmt.Errorf("trusted Go module tag %q does not resolve to the requested commit", config.Version)
	}
	if cleanErr := requireCleanCheckout(ctx, root); cleanErr != nil {
		return sourceIdentity{}, cleanErr
	}
	remote, err := gitOutput(ctx, root, 4096, "remote", "get-url", "origin")
	if err != nil {
		return sourceIdentity{}, fmt.Errorf("read trusted Go module origin: %w", err)
	}
	canonical, repository, err := canonicalSourceURL(string(remote))
	if err != nil {
		return sourceIdentity{}, fmt.Errorf("parse trusted Go module origin: %w", err)
	}
	if canonical != policy.source || repository != policy.repository || canonical != config.CanonicalSource {
		return sourceIdentity{}, fmt.Errorf("trusted Go module origin %q does not match policy source %q", canonical, policy.source)
	}
	epochData, err := gitOutput(ctx, root, 128, "show", "-s", "--format=%ct", config.Commit)
	if err != nil {
		return sourceIdentity{}, fmt.Errorf("read trusted Go module commit epoch: %w", err)
	}
	seconds, err := strconv.ParseInt(strings.TrimSpace(string(epochData)), 10, 64)
	if err != nil || seconds <= 0 {
		return sourceIdentity{}, errors.New("trusted Go module commit has an invalid epoch")
	}
	treeData, err := gitOutput(ctx, root, maxGitTree, "ls-tree", "-rz", "--full-tree", config.Commit)
	if err != nil {
		return sourceIdentity{}, fmt.Errorf("read trusted Go module tree: %w", err)
	}
	entries, err := parseGitTree(treeData)
	if err != nil {
		return sourceIdentity{}, err
	}
	return sourceIdentity{
		root: root, commit: config.Commit, epoch: time.Unix(seconds, 0).UTC(),
		source: canonical, entries: entries,
	}, nil
}

func validateExactCommit(commit string) error {
	if len(commit) != 40 && len(commit) != 64 {
		return errors.New("verify Go module release: commit must be an exact 40- or 64-hex object ID")
	}
	if _, err := hex.DecodeString(commit); err != nil || commit != strings.ToLower(commit) {
		return errors.New("verify Go module release: commit must use lowercase hexadecimal")
	}
	return nil
}

func realDirectory(configured, label string) (string, error) {
	if strings.TrimSpace(configured) == "" {
		return "", fmt.Errorf("verify Go module release: %s is required", label)
	}
	absolute, err := filepath.Abs(configured)
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", label, err)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", label, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("%s %q is not a real directory", label, absolute)
	}
	return absolute, nil
}

func canonicalSourceURL(value string) (string, string, error) {
	value = strings.TrimSpace(value)
	if before, after, found := strings.Cut(value, ":"); found &&
		!strings.Contains(before, "/") && strings.Contains(before, "@") {
		value = "ssh://" + before + "/" + after
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "", "", err
	}
	if !slices.Contains([]string{"https", "ssh"}, parsed.Scheme) || parsed.Hostname() == "" ||
		parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", "", errors.New("require an HTTPS or SSH origin without query or fragment")
	}
	if parsed.Scheme == "https" && parsed.User != nil {
		return "", "", errors.New("origin HTTPS URL must not contain credentials")
	}
	if parsed.Scheme == "ssh" && (parsed.User == nil || parsed.User.Username() != "git") {
		return "", "", errors.New("origin SSH URL must use the git user")
	}
	repositoryPath := strings.TrimSuffix(strings.Trim(parsed.Path, "/"), ".git")
	if repositoryPath == "" || path.Clean(repositoryPath) != repositoryPath ||
		repositoryPath == ".." || strings.HasPrefix(repositoryPath, "../") {
		return "", "", errors.New("origin repository path is empty or unsafe")
	}
	repository := path.Base(repositoryPath)
	if !safeName(repository) {
		return "", "", fmt.Errorf("origin repository name %q is unsafe", repository)
	}
	host := strings.ToLower(parsed.Hostname())
	if parsed.Port() != "" {
		host += ":" + parsed.Port()
	}
	return "https://" + host + "/" + repositoryPath, repository, nil
}

func parseGitTree(data []byte) ([]gitEntry, error) {
	if len(data) == 0 || len(data) > maxGitTree {
		return nil, errors.New("trusted Go module tree is empty or exceeds limits")
	}
	records := bytes.Split(bytes.TrimSuffix(data, []byte{0}), []byte{0})
	if len(records) > maxTreeEntries {
		return nil, errors.New("trusted Go module tree exceeds entry limits")
	}
	entries := make([]gitEntry, 0, len(records))
	portable := make(map[string]string, len(records))
	for index, record := range records {
		metadata, name, found := bytes.Cut(record, []byte{'\t'})
		fields := bytes.Fields(metadata)
		if !found || len(fields) != 3 || string(fields[1]) != "blob" {
			return nil, fmt.Errorf("trusted Go module tree entry %d has unsupported metadata", index)
		}
		filename := string(name)
		if err := validatePortablePath(filename); err != nil {
			return nil, fmt.Errorf("trusted Go module source path %q: %w", filename, err)
		}
		key := strings.ToLower(filename)
		if prior, found := portable[key]; found {
			return nil, fmt.Errorf("trusted Go module paths %q and %q collide", prior, filename)
		}
		portable[key] = filename
		entry := gitEntry{name: filename, objectID: string(fields[2]), mode: string(fields[0])}
		if err := validateObjectID(entry.objectID); err != nil {
			return nil, fmt.Errorf("trusted Go module path %q: %w", filename, err)
		}
		switch entry.mode {
		case "100644", "100755":
		case "120000":
			return nil, fmt.Errorf("trusted Go module path %q is a symlink; symlinks are not permitted", filename)
		default:
			return nil, fmt.Errorf("trusted Go module path %q has unsupported mode %q", filename, entry.mode)
		}
		entries = append(entries, entry)
	}
	slices.SortFunc(entries, func(left, right gitEntry) int { return strings.Compare(left.name, right.name) })
	return entries, nil
}

func validateObjectID(value string) error {
	if len(value) != 40 && len(value) != 64 {
		return fmt.Errorf("git object ID %q has unsupported length", value)
	}
	if _, err := hex.DecodeString(value); err != nil {
		return fmt.Errorf("git object ID %q is invalid", value)
	}
	return nil
}

func validatePortablePath(name string) error {
	if name == "" || !utf8.ValidString(name) || path.IsAbs(name) || path.Clean(name) != name ||
		name == "." || !filepath.IsLocal(filepath.FromSlash(name)) || strings.Contains(name, "\\") {
		return errors.New("path is empty, absolute, contains traversal, or is invalid UTF-8")
	}
	for component := range strings.SplitSeq(name, "/") {
		if !portableComponent(component) {
			return errors.New("path is not portable printable ASCII")
		}
	}
	return nil
}

func portableComponent(component string) bool {
	if component == "" || component == "." || component == ".." ||
		strings.HasSuffix(component, ".") || strings.HasSuffix(component, " ") {
		return false
	}
	for index := range len(component) {
		character := component[index]
		if character < 0x20 || character > 0x7e || strings.ContainsRune(`<>:"|?*`, rune(character)) {
			return false
		}
	}
	base, _, _ := strings.Cut(component, ".")
	switch strings.ToUpper(base) {
	case "CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return false
	default:
		return true
	}
}

func expectedSourceArchive(ctx context.Context, source sourceIdentity, repository, version string) ([]byte, error) {
	prefix := repository + "_" + strings.TrimPrefix(version, "v") + "/"
	tarData, err := gitOutput(ctx, source.root, maxArchiveSource, "archive", "--format=tar", "--prefix="+prefix, source.commit)
	if err != nil {
		return nil, fmt.Errorf("reproduce trusted Go module archive: %w", err)
	}
	var output bytes.Buffer
	writer, err := gzip.NewWriterLevel(&output, gzip.BestCompression)
	if err != nil {
		return nil, fmt.Errorf("create trusted Go module gzip: %w", err)
	}
	writer.ModTime = source.epoch
	writer.OS = 255
	if _, err := writer.Write(tarData); err != nil {
		return nil, errors.Join(err, writer.Close())
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	if output.Len() > maxArchiveBytes {
		return nil, errors.New("trusted Go module archive exceeds limits")
	}
	return output.Bytes(), nil
}

func requireCleanCheckout(ctx context.Context, root string) error {
	status, err := gitOutput(ctx, root, 4096, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return fmt.Errorf("inspect trusted Go module checkout: %w", err)
	}
	if len(bytes.TrimSpace(status)) != 0 {
		return errors.New("trusted Go module checkout must be clean, including untracked files")
	}
	return nil
}

func revalidateSource(ctx context.Context, source sourceIdentity, version string) error {
	head, err := gitOutput(ctx, source.root, 128, "rev-parse", "--verify", "HEAD^{commit}")
	if err != nil || strings.TrimSpace(string(head)) != source.commit {
		return errors.New("trusted Go module checkout changed during verification")
	}
	tag, err := gitOutput(ctx, source.root, 128, "rev-parse", "--verify", "refs/tags/"+version+"^{commit}")
	if err != nil || strings.TrimSpace(string(tag)) != source.commit {
		return errors.New("trusted Go module tag changed during verification")
	}
	return requireCleanCheckout(ctx, source.root)
}

func gitOutput(ctx context.Context, root string, maximum int, arguments ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(arguments) == 0 {
		return nil, errors.New("git command requires an argument")
	}
	// #nosec G204 -- executable is fixed; object IDs and policy-bound values are validated.
	command := exec.CommandContext(ctx, "git", arguments...)
	command.Dir = root
	command.Env = append(gitenv.ReadOnly(os.Environ()), "GIT_NO_LAZY_FETCH=1")
	var stdout, stderr boundedBuffer
	stdout.maximum = maximum
	stderr.maximum = maxDiagnostic
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("git %s: %w: %s", arguments[0], err, stderr.String())
	}
	if stdout.truncated {
		return nil, fmt.Errorf("git %s output exceeds %d bytes", arguments[0], maximum)
	}
	return slices.Clone(stdout.Bytes()), nil
}

type boundedBuffer struct {
	content   bytes.Buffer
	maximum   int
	truncated bool
}

func (buffer *boundedBuffer) Write(content []byte) (int, error) {
	written := len(content)
	remaining := buffer.maximum - buffer.content.Len()
	if remaining <= 0 {
		buffer.truncated = true
		return written, nil
	}
	if len(content) > remaining {
		content = content[:remaining]
		buffer.truncated = true
	}
	_, _ = buffer.content.Write(content)
	return written, nil
}

func (buffer *boundedBuffer) Bytes() []byte { return buffer.content.Bytes() }

func (buffer *boundedBuffer) String() string { return strings.TrimSpace(buffer.content.String()) }

func readGitBlob(ctx context.Context, source sourceIdentity, name string, maximum int64) ([]byte, error) {
	entry, found := findEntry(source.entries, name)
	if !found || entry.mode != "100644" {
		return nil, fmt.Errorf("required committed Go module file %q is not a regular 100644 blob", name)
	}
	if maximum > int64(^uint(0)>>1) {
		return nil, errors.New("requested Git blob limit exceeds host integer size")
	}
	return gitOutput(ctx, source.root, int(maximum), "cat-file", "blob", entry.objectID)
}

func findEntry(entries []gitEntry, name string) (gitEntry, bool) {
	index, found := slices.BinarySearchFunc(entries, name, func(entry gitEntry, value string) int {
		return strings.Compare(entry.name, value)
	})
	if !found {
		return gitEntry{}, false
	}
	return entries[index], true
}
