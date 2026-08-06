package release

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

type decodedSourceEntry struct {
	mode       int64
	typeFlag   byte
	linkTarget string
	data       []byte
}

func TestSourceSnapshotIgnoresWorktreeAndPreservesGitMetadata(t *testing.T) {
	t.Parallel()

	root := initializeSnapshotRepository(t, map[string]string{
		"go.mod":         "module example.com/snapshot\n\ngo 1.26.0\n",
		"LICENSE":        "head license\n",
		"README.md":      "head readme\n",
		"scripts/run.sh": "#!/bin/sh\necho head\n",
	})
	runGit(t, root, "add", ".")
	runGit(t, root, "update-index", "--chmod=+x", "scripts/run.sh")
	linkBlob := runGitInput(t, root, "README.md", "hash-object", "-w", "--stdin")
	runGit(
		t,
		root,
		"update-index",
		"--add",
		"--cacheinfo",
		gitModeSymlink+","+linkBlob+",README.link",
	)
	commitSnapshotRepository(t, root)

	writeSnapshotFile(t, root, "README.md", "dirty readme\n", 0o600)
	writeSnapshotFile(t, root, "untracked-secret.txt", "must not ship\n", 0o600)
	snapshot, err := captureSourceSnapshot(t.Context(), root, "HEAD")
	if err != nil {
		t.Fatalf("captureSourceSnapshot() error = %v", err)
	}
	readme, err := snapshot.file("README.md")
	if err != nil || string(readme) != "head readme\n" {
		t.Fatalf("snapshot README = %q, %v", readme, err)
	}
	if _, untrackedErr := snapshot.file("untracked-secret.txt"); untrackedErr == nil {
		t.Fatal("untracked worktree file entered source snapshot")
	}
	entry := snapshotEntryNamed(t, snapshot, "scripts/run.sh")
	if entry.mode != 0o755 || entry.kind != snapshotRegular {
		t.Fatalf("executable snapshot entry = %+v", entry)
	}
	link := snapshotEntryNamed(t, snapshot, "README.link")
	if link.kind != snapshotSymlink || link.linkTarget != "README.md" {
		t.Fatalf("symlink snapshot entry = %+v", link)
	}

	buildRoot := t.TempDir()
	if materializeErr := snapshot.materialize(buildRoot); materializeErr != nil {
		t.Fatalf("materialize() error = %v", materializeErr)
	}
	materialized, err := os.ReadFile(filepath.Join(buildRoot, "README.md"))
	if err != nil || string(materialized) != "head readme\n" {
		t.Fatalf("materialized README = %q, %v", materialized, err)
	}
	if _, statErr := os.Stat(filepath.Join(buildRoot, "untracked-secret.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("untracked file materialized: %v", statErr)
	}
	linkTarget, err := os.Readlink(filepath.Join(buildRoot, "README.link"))
	if err != nil || filepath.ToSlash(linkTarget) != "README.md" {
		t.Fatalf("materialized symlink = %q, %v", linkTarget, err)
	}
	if runtime.GOOS != "windows" {
		info, statErr := os.Stat(filepath.Join(buildRoot, "scripts", "run.sh"))
		if statErr != nil {
			t.Fatal(statErr)
		}
		if info.Mode().Perm() != 0o755 {
			t.Fatalf("materialized executable mode = %v", info.Mode())
		}
	}

	archive := filepath.Join(t.TempDir(), "source.tar.gz")
	if err := writeArchive(
		archive,
		Target{GOOS: "linux", GOARCH: "source"},
		time.Unix(123, 0).UTC(),
		snapshot.archiveEntries("spice-1.0.0"),
	); err != nil {
		t.Fatalf("write source archive: %v", err)
	}
	entries := decodeSourceArchive(t, archive)
	if got := string(entries["spice-1.0.0/README.md"].data); got != "head readme\n" {
		t.Fatalf("archived README = %q", got)
	}
	if _, found := entries["spice-1.0.0/untracked-secret.txt"]; found {
		t.Fatal("untracked file entered source archive")
	}
	if got := entries["spice-1.0.0/scripts/run.sh"]; got.mode != 0o755 || got.typeFlag != tar.TypeReg {
		t.Fatalf("archived executable = %+v", got)
	}
	if got := entries["spice-1.0.0/README.link"]; got.typeFlag != tar.TypeSymlink ||
		got.linkTarget != "README.md" {
		t.Fatalf("archived symlink = %+v", got)
	}
}

func TestBuildUsesOneExactCommitSnapshotAfterHEADMoves(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	root := filepath.Join(parent, "repository")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	initializeSnapshotRepositoryAt(t, root, map[string]string{
		"go.mod":    "module github.com/spice-framework/toolchain\n\ngo 1.26.0\n",
		"LICENSE":   "head license\n",
		"README.md": "head readme\n",
		"cmd/spice/main.go": `package main

import "fmt"

func main() { fmt.Print("HEAD-BINARY") }
`,
		"vendor/modules.txt": "",
	})
	runGit(t, root, "add", ".")
	commitSnapshotRepository(t, root)
	commit := runGit(t, root, "rev-parse", "HEAD")

	writeSnapshotFile(t, root, "README.md", "dirty readme\n", 0o600)
	writeSnapshotFile(t, root, "LICENSE", "dirty license\n", 0o600)
	writeSnapshotFile(t, root, "cmd/spice/main.go", "this is invalid Go\n", 0o600)
	writeSnapshotFile(t, root, "vendor/modules.txt", "# dirty.invalid v9.9.9\n", 0o600)
	runGit(t, root, "add", ".")
	commitSnapshotRepository(t, root)
	writeSnapshotFile(t, root, "private-key.txt", "untracked\n", 0o600)

	result, err := Build(t.Context(), Config{
		Root:          root,
		OutputDir:     filepath.Join(parent, "release"),
		Version:       "v1.2.3",
		Commit:        commit,
		Epoch:         time.Unix(1_788_000_000, 0).UTC(),
		Targets:       []Target{HostTarget()},
		AllowUnsigned: true,
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	binaryArchive := releaseBinaryArchive(t, result)
	binaryEntries := readArchive(t, binaryArchive, HostTarget())
	if string(binaryEntries["README.md"]) != "head readme\n" ||
		string(binaryEntries["LICENSE"]) != "head license\n" {
		t.Fatalf("binary archive used mutable worktree: %#v", binaryEntries)
	}
	binary := filepath.Join(t.TempDir(), HostTarget().ExecutableName())
	if writeErr := os.WriteFile(binary, binaryEntries[HostTarget().ExecutableName()], 0o755); writeErr != nil {
		t.Fatal(writeErr)
	}
	output, err := exec.CommandContext(t.Context(), binary).CombinedOutput()
	if err != nil || string(output) != "HEAD-BINARY" {
		t.Fatalf("snapshotted binary output = %q, %v", output, err)
	}

	sourceArchive := filepath.Join(result.OutputDir, "spice_1.2.3_source.tar.gz")
	sourceEntries := decodeSourceArchive(t, sourceArchive)
	if string(sourceEntries["spice-1.2.3/README.md"].data) != "head readme\n" ||
		!strings.Contains(
			string(sourceEntries["spice-1.2.3/cmd/spice/main.go"].data),
			"HEAD-BINARY",
		) {
		t.Fatalf("source archive used mutable worktree: %#v", sourceEntries)
	}
	if _, found := sourceEntries["spice-1.2.3/private-key.txt"]; found {
		t.Fatal("untracked file entered release source archive")
	}

	sbomData, err := os.ReadFile(filepath.Join(result.OutputDir, "spice_1.2.3_sbom.spdx.json"))
	if err != nil {
		t.Fatal(err)
	}
	var sbom spdxDocument
	if err := json.Unmarshal(sbomData, &sbom); err != nil {
		t.Fatal(err)
	}
	if len(sbom.Packages) != 1 || sbom.Packages[0].Name != "github.com/spice-framework/toolchain" {
		t.Fatalf("SBOM used mutable vendor graph: %+v", sbom.Packages)
	}
}

func TestSourceSnapshotRejectsGitlinksAndUnsafeSymlinks(t *testing.T) {
	t.Parallel()

	t.Run("gitlink", func(t *testing.T) {
		root := initializeSnapshotRepository(t, map[string]string{"README.md": "readme\n"})
		runGit(t, root, "add", ".")
		commitSnapshotRepository(t, root)
		commit := runGit(t, root, "rev-parse", "HEAD")
		runGit(
			t,
			root,
			"update-index",
			"--add",
			"--cacheinfo",
			gitModeGitlink+","+commit+",dependency",
		)
		commitSnapshotRepository(t, root)
		if _, err := captureSourceSnapshot(t.Context(), root, "HEAD"); err == nil ||
			!strings.Contains(err.Error(), "gitlink") {
			t.Fatalf("capture gitlink error = %v", err)
		}
	})

	for _, target := range []string{"../outside", "/absolute", `C:/absolute`} {
		linkTarget := target
		t.Run("symlink-"+strings.NewReplacer("/", "-", ":", "-").Replace(linkTarget), func(t *testing.T) {
			root := initializeSnapshotRepository(t, map[string]string{"README.md": "readme\n"})
			runGit(t, root, "add", ".")
			linkBlob := runGitInput(t, root, linkTarget, "hash-object", "-w", "--stdin")
			runGit(
				t,
				root,
				"update-index",
				"--add",
				"--cacheinfo",
				gitModeSymlink+","+linkBlob+",unsafe.link",
			)
			commitSnapshotRepository(t, root)
			if _, err := captureSourceSnapshot(t.Context(), root, "HEAD"); err == nil ||
				!strings.Contains(err.Error(), "unsafe") && !strings.Contains(err.Error(), "escapes") {
				t.Fatalf("capture unsafe symlink error = %v", err)
			}
		})
	}
}

func TestSourceSnapshotIgnoresGitReplacementObjects(t *testing.T) {
	t.Parallel()
	root := initializeSnapshotRepository(t, map[string]string{
		"README.md": "trusted\n",
	})
	runGit(t, root, "add", ".")
	commitSnapshotRepository(t, root)
	trusted := runGit(t, root, "rev-parse", "HEAD")
	writeSnapshotFile(t, root, "README.md", "replacement\n", 0o600)
	runGit(t, root, "add", ".")
	commitSnapshotRepository(t, root)
	replacement := runGit(t, root, "rev-parse", "HEAD")
	runGit(t, root, "reset", "--hard", trusted)
	runGit(t, root, "replace", trusted, replacement)

	snapshot, err := captureSourceSnapshot(t.Context(), root, trusted)
	if err != nil {
		t.Fatal(err)
	}
	readme, err := snapshot.file("README.md")
	if err != nil || string(readme) != "trusted\n" {
		t.Fatalf("replacement-safe README = %q, %v", readme, err)
	}
}

func TestSnapshotTreeParserRejectsUnsafePathsAndModes(t *testing.T) {
	t.Parallel()

	objectID := strings.Repeat("a", 40)
	for _, record := range []string{
		"100664 blob " + objectID + "\tfile.go\x00",
		"100644 blob " + objectID + "\t../escape.go\x00",
		"100644 blob " + objectID + "\tC:/escape.go\x00",
		"100644 blob " + objectID + "\tdir\\escape.go\x00",
		"100644 tree " + objectID + "\tfile.go\x00",
	} {
		if _, err := parseSnapshotTree([]byte(record)); err == nil {
			t.Fatalf("parseSnapshotTree(%q) error = nil", record)
		}
	}
	caseCollision := "100644 blob " + objectID + "\tREADME.md\x00" +
		"100644 blob " + strings.Repeat("b", 40) + "\treadme.md\x00"
	if _, err := parseSnapshotTree([]byte(caseCollision)); err == nil ||
		!strings.Contains(err.Error(), "case-insensitive") {
		t.Fatalf("parseSnapshotTree(case collision) error = %v", err)
	}
}

func TestRequiredReleaseFilesMustExistAtHEAD(t *testing.T) {
	t.Parallel()

	root := initializeSnapshotRepository(t, map[string]string{"tracked.txt": "tracked\n"})
	runGit(t, root, "add", ".")
	commitSnapshotRepository(t, root)
	for filename, content := range map[string]string{
		"go.mod":    "module example.com/untracked\n",
		"LICENSE":   "untracked license\n",
		"README.md": "untracked readme\n",
	} {
		writeSnapshotFile(t, root, filename, content, 0o600)
	}
	snapshot, err := captureSourceSnapshot(t.Context(), root, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if err := validateRequiredSnapshotFiles(snapshot); err == nil ||
		!strings.Contains(err.Error(), "required source file") {
		t.Fatalf("validateRequiredSnapshotFiles() error = %v", err)
	}
}

func initializeSnapshotRepository(
	t *testing.T,
	files map[string]string,
) string {
	t.Helper()
	root := t.TempDir()
	initializeSnapshotRepositoryAt(t, root, files)
	return root
}

func initializeSnapshotRepositoryAt(
	t *testing.T,
	root string,
	files map[string]string,
) {
	t.Helper()
	runGit(t, root, "init", "--quiet")
	runGit(t, root, "config", "core.autocrlf", "false")
	runGit(t, root, "config", "user.name", "Spice Tests")
	runGit(t, root, "config", "user.email", "spice-tests@example.invalid")
	for filename, content := range files {
		writeSnapshotFile(t, root, filename, content, 0o600)
	}
}

func writeSnapshotFile(
	t *testing.T,
	root string,
	filename string,
	content string,
	mode os.FileMode,
) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(filename))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func commitSnapshotRepository(t *testing.T, root string) {
	t.Helper()
	runGit(t, root, "commit", "--quiet", "-m", "snapshot fixture")
}

func runGit(t *testing.T, root string, arguments ...string) string {
	t.Helper()
	command := exec.CommandContext(t.Context(), "git", arguments...)
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", arguments, err, output)
	}
	return strings.TrimSpace(string(output))
}

func runGitInput(
	t *testing.T,
	root string,
	input string,
	arguments ...string,
) string {
	t.Helper()
	command := exec.CommandContext(t.Context(), "git", arguments...)
	command.Dir = root
	command.Stdin = strings.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", arguments, err, output)
	}
	return strings.TrimSpace(string(output))
}

func snapshotEntryNamed(
	t *testing.T,
	snapshot sourceSnapshot,
	name string,
) snapshotEntry {
	t.Helper()
	index := slices.IndexFunc(snapshot.entries, func(entry snapshotEntry) bool {
		return entry.path == name
	})
	if index < 0 {
		t.Fatalf("snapshot entry %q not found", name)
	}
	return snapshot.entries[index]
}

func releaseBinaryArchive(t *testing.T, result Result) string {
	t.Helper()
	target := HostTarget()
	needle := "_" + target.GOOS + "_" + target.GOARCH + target.ArchiveExtension()
	for _, filename := range result.Files {
		if strings.HasSuffix(filename, needle) {
			return filepath.Join(result.OutputDir, filename)
		}
	}
	t.Fatalf("release has no binary archive for %s: %v", target, result.Files)
	return ""
}

func decodeSourceArchive(
	t *testing.T,
	filename string,
) map[string]decodedSourceEntry {
	t.Helper()
	file, err := os.Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			t.Errorf("close source archive: %v", closeErr)
		}
	}()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := gzipReader.Close(); closeErr != nil {
			t.Errorf("close source gzip: %v", closeErr)
		}
	}()
	reader := tar.NewReader(gzipReader)
	entries := make(map[string]decodedSourceEntry)
	for {
		header, readErr := reader.Next()
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			t.Fatal(readErr)
		}
		data, readErr := io.ReadAll(reader)
		if readErr != nil {
			t.Fatal(readErr)
		}
		entries[header.Name] = decodedSourceEntry{
			mode:       header.Mode,
			typeFlag:   header.Typeflag,
			linkTarget: header.Linkname,
			data:       data,
		}
	}
	return entries
}

func TestCaptureSourceSnapshotRejectsCanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := captureSourceSnapshot(ctx, t.TempDir(), "HEAD")
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("captureSourceSnapshot(canceled) error = %v", err)
	}
}
