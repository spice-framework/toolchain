package goreleaseverify

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type archiveTestEntry struct {
	header  tar.Header
	content []byte
}

func TestJSONBoundariesFailClosed(t *testing.T) {
	t.Parallel()
	var decoded map[string]any
	if err := decodeStrictJSON([]byte("{\"value\":1}\n"), &decoded); err != nil {
		t.Fatalf("decodeStrictJSON(valid) error = %v", err)
	}
	for _, document := range []string{
		"", "{} {}", "{", "{\"value\":1,\"value\":2}", "{\"outer\":{\"value\":1,\"value\":2}}",
	} {
		if err := decodeStrictJSON([]byte(document), &decoded); err == nil {
			t.Errorf("decodeStrictJSON(%q) error = nil", document)
		}
	}
	deep := strings.Repeat("[", maxJSONDepth+2) + strings.Repeat("]", maxJSONDepth+2)
	if err := rejectDuplicateJSONKeys([]byte(deep)); err == nil || !strings.Contains(err.Error(), "nesting depth") {
		t.Fatalf("rejectDuplicateJSONKeys(deep) error = %v", err)
	}
	for _, document := range []string{
		"[]", "[{},[1,true,null]]", "{\"outer\":{\"value\":1}}", "{\"value\":1} trailing",
	} {
		err := rejectDuplicateJSONKeys([]byte(document))
		if document == "{\"value\":1} trailing" {
			if err == nil {
				t.Errorf("rejectDuplicateJSONKeys(%q) error = nil", document)
			}
			continue
		}
		if err != nil {
			t.Errorf("rejectDuplicateJSONKeys(%q) error = %v", document, err)
		}
	}
}

func TestModuleGraphHelpersFailClosed(t *testing.T) {
	t.Parallel()
	for version, want := range map[string]bool{
		"v1.2.3": true, "v1.2.3+build": true, "v1.2": false, "1.2.3": false,
	} {
		if actual := canonicalVersion(version); actual != want {
			t.Errorf("canonicalVersion(%q) = %t, want %t", version, actual, want)
		}
	}
	validVendor := []byte("# example.com/a v1.2.3\n## explicit; go 1.26.0\nexample.com/a/pkg\n")
	modules, err := parseVendorModules(validVendor)
	if err != nil || len(modules) != 1 || modules[0].path != "example.com/a" {
		t.Fatalf("parseVendorModules(valid) = %#v, %v", modules, err)
	}
	for _, content := range [][]byte{
		nil,
		[]byte("# example.com/a invalid\n## explicit\n"),
		[]byte("# example.com/a v1.2.3\n"),
		[]byte("# example.com/a v1.2.3 replacement\n## explicit\n"),
		[]byte("# example.com/a v1.2.3\n## explicit\n# example.com/a v1.2.3\n## explicit\n"),
	} {
		if _, parseErr := parseVendorModules(content); parseErr == nil {
			t.Errorf("parseVendorModules(%q) error = nil", content)
		}
	}
	if vendorExplicit("## go 1.26.0") || !vendorExplicit("## explicit; go 1.26.0") {
		t.Fatal("vendorExplicit() did not enforce explicit metadata")
	}

	expected := []selectedModule{{path: "example.com/a", version: "v1.2.3"}}
	if err := compareModuleGraph(expected, expected); err != nil {
		t.Fatalf("compareModuleGraph(equal) error = %v", err)
	}
	for _, actual := range [][]selectedModule{
		nil,
		{{path: "example.com/a", version: "v1.2.4"}},
		{{path: "example.com/b", version: "v1.2.3"}},
	} {
		if err := compareModuleGraph(expected, actual); err == nil {
			t.Errorf("compareModuleGraph(%#v) error = nil", actual)
		}
	}
}

func TestAuthenticationFileWriterIsDirectoryScoped(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	outside := filepath.Join(filepath.Dir(root), "outside.sum")
	if err := writeAuthenticationFile(root, "../outside.sum", []byte("untrusted")); err == nil {
		t.Fatal("writeAuthenticationFile(parent traversal) error = nil")
	}
	if _, err := os.Lstat(outside); !os.IsNotExist(err) {
		t.Fatalf("authentication writer escaped its root: %v", err)
	}
	if err := writeAuthenticationFile(root, authenticationSumFile, []byte("trusted\n")); err != nil {
		t.Fatalf("writeAuthenticationFile(valid) error = %v", err)
	}
	if err := writeAuthenticationFile(root, authenticationSumFile, []byte("replacement\n")); err == nil {
		t.Fatal("writeAuthenticationFile(existing) error = nil")
	}
}

func TestSourceIdentityHelpersFailClosed(t *testing.T) {
	t.Parallel()
	validCommit := strings.Repeat("a", 40)
	if err := validateExactCommit(validCommit); err != nil {
		t.Fatalf("validateExactCommit(valid) error = %v", err)
	}
	for _, commit := range []string{"", strings.Repeat("a", 39), strings.Repeat("A", 40), strings.Repeat("z", 40)} {
		if err := validateExactCommit(commit); err == nil {
			t.Errorf("validateExactCommit(%q) error = nil", commit)
		}
	}

	directory := t.TempDir()
	file := filepath.Join(directory, "file")
	if err := os.WriteFile(file, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if actual, err := realDirectory(directory, "fixture"); err != nil || !filepath.IsAbs(actual) {
		t.Fatalf("realDirectory(valid) = %q, %v", actual, err)
	}
	for _, candidate := range []string{"", file, filepath.Join(directory, "missing")} {
		if _, err := realDirectory(candidate, "fixture"); err == nil {
			t.Errorf("realDirectory(%q) error = nil", candidate)
		}
	}

	for input, want := range map[string]string{
		"https://github.com/spice-framework/spice-agent.git":   "https://github.com/spice-framework/spice-agent",
		"ssh://git@github.com/spice-framework/spice-agent.git": "https://github.com/spice-framework/spice-agent",
		"git@github.com:spice-framework/spice-agent.git":       "https://github.com/spice-framework/spice-agent",
	} {
		canonical, repository, err := canonicalSourceURL(input)
		if err != nil || canonical != want || repository != "spice-agent" {
			t.Errorf("canonicalSourceURL(%q) = %q, %q, %v", input, canonical, repository, err)
		}
	}
	for _, input := range []string{
		"http://github.com/org/repo", "https://user@github.com/org/repo",
		"ssh://user@github.com/org/repo", "https://github.com/org/repo?ref=main",
		"https://github.com/..", "not a URL",
	} {
		if _, _, err := canonicalSourceURL(input); err == nil {
			t.Errorf("canonicalSourceURL(%q) error = nil", input)
		}
	}
}

func TestGitTreeAndPortablePathBoundaries(t *testing.T) {
	t.Parallel()
	oid := strings.Repeat("a", 40)
	valid := []byte("100644 blob " + oid + "\tREADME.md\x00100755 blob " + oid + "\tcmd/tool\x00")
	entries, err := parseGitTree(valid)
	if err != nil || len(entries) != 2 || entries[0].name != "README.md" {
		t.Fatalf("parseGitTree(valid) = %#v, %v", entries, err)
	}
	for _, tree := range [][]byte{
		nil,
		[]byte("100644 tree " + oid + "\tREADME.md\x00"),
		[]byte("100644 blob " + oid + " README.md\x00"),
		[]byte("100644 blob short\tREADME.md\x00"),
		[]byte("100600 blob " + oid + "\tREADME.md\x00"),
		[]byte("120000 blob " + oid + "\tlatest\x00"),
		[]byte("100644 blob " + oid + "\t../escape\x00"),
		[]byte("100644 blob " + oid + "\tReadme.md\x00100644 blob " + oid + "\tREADME.md\x00"),
	} {
		if _, parseErr := parseGitTree(tree); parseErr == nil {
			t.Errorf("parseGitTree(%q) error = nil", tree)
		}
	}
	for _, oidValue := range []string{"", strings.Repeat("g", 40)} {
		if err := validateObjectID(oidValue); err == nil {
			t.Errorf("validateObjectID(%q) error = nil", oidValue)
		}
	}
	if err := validateObjectID(oid); err != nil {
		t.Fatalf("validateObjectID(valid) error = %v", err)
	}
	for pathValue, want := range map[string]bool{
		"README.md": true, "cmd/tool": true, "": false, "../escape": false,
		"bad\\path": false, "CON": false, "dir/trailing.": false, "bad:name": false,
	} {
		err := validatePortablePath(pathValue)
		if (err == nil) != want {
			t.Errorf("validatePortablePath(%q) error = %v, valid want %t", pathValue, err, want)
		}
	}
}

func TestChecksumAndBufferBoundaries(t *testing.T) {
	t.Parallel()
	content := []byte("content")
	digest := sha256.Sum256(content)
	name := "artifact"
	canonical := hex.EncodeToString(digest[:]) + "  " + name + "\n"
	artifacts := map[string][]byte{"checksums.txt": []byte(canonical), name: content}
	if err := verifyChecksums(artifacts, []string{name}); err != nil {
		t.Fatalf("verifyChecksums(valid) error = %v", err)
	}
	for _, checksums := range []string{
		"", strings.TrimSuffix(canonical, "\n"), strings.ReplaceAll(canonical, "\n", "\r\n"),
		canonical + canonical, "bad  " + name + "\n", strings.Repeat("0", 64) + "  " + name + "\n",
	} {
		candidate := map[string][]byte{"checksums.txt": []byte(checksums), name: content}
		if err := verifyChecksums(candidate, []string{name}); err == nil {
			t.Errorf("verifyChecksums(%q) error = nil", checksums)
		}
	}

	buffer := boundedBuffer{maximum: 3}
	if written, err := buffer.Write([]byte("ab")); err != nil || written != 2 {
		t.Fatalf("Write(first) = %d, %v", written, err)
	}
	if written, err := buffer.Write([]byte("cd")); err != nil || written != 2 {
		t.Fatalf("Write(second) = %d, %v", written, err)
	}
	if buffer.String() != "abc" || !buffer.truncated {
		t.Fatalf("buffer = %q, truncated %t", buffer.String(), buffer.truncated)
	}
	if written, err := buffer.Write([]byte("more")); err != nil || written != 4 {
		t.Fatalf("Write(full) = %d, %v", written, err)
	}
	if _, err := marshalCanonical(make(chan int)); err == nil {
		t.Fatal("marshalCanonical(channel) error = nil")
	}
	for value, want := range map[string]bool{"safe": true, "": false, ".": false, "a/b": false, "a:b": false} {
		if actual := safeName(value); actual != want {
			t.Errorf("safeName(%q) = %t, want %t", value, actual, want)
		}
	}

	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	runner, runnerErr := newSystemGoRunner()
	if runnerErr != nil {
		t.Fatalf("newSystemGoRunner() error = %v", runnerErr)
	}
	if _, err := runner.Output(canceled, t.TempDir(), os.Environ(), "version"); err == nil {
		t.Fatal("Go runner(canceled) error = nil")
	}
	if _, err := gitOutput(canceled, t.TempDir(), maxDiagnostic, "version"); err == nil {
		t.Fatal("gitOutput(canceled) error = nil")
	}
}

func TestArtifactFileBoundaries(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	writeFile(t, filepath.Join(directory, "artifact"), []byte("data"))
	content, err := readBoundedRegularFile(directory, "artifact", 4)
	if err != nil || string(content) != "data" {
		t.Fatalf("readBoundedRegularFile(valid) = %q, %v", content, err)
	}
	for _, test := range []struct {
		name    string
		maximum int64
	}{
		{name: "missing", maximum: 4},
		{name: "artifact", maximum: 3},
	} {
		if _, readErr := readBoundedRegularFile(directory, test.name, test.maximum); readErr == nil {
			t.Errorf("readBoundedRegularFile(%q, %d) error = nil", test.name, test.maximum)
		}
	}
	if mkdirErr := os.Mkdir(filepath.Join(directory, "nested"), 0o700); mkdirErr != nil {
		t.Fatal(mkdirErr)
	}
	if _, readErr := readBoundedRegularFile(directory, "nested", maxArtifactBytes); readErr == nil {
		t.Fatal("readBoundedRegularFile(directory) error = nil")
	}

	artifacts, resolved, err := readArtifactSet(directory, []string{"artifact", "nested"})
	if err == nil || artifacts != nil || resolved != "" {
		t.Fatalf("readArtifactSet(directory entry) = %#v, %q, %v", artifacts, resolved, err)
	}
	if removeErr := os.Remove(filepath.Join(directory, "nested")); removeErr != nil {
		t.Fatal(removeErr)
	}
	artifacts, resolved, err = readArtifactSet(directory, []string{"artifact"})
	if err != nil || string(artifacts["artifact"]) != "data" || resolved == "" {
		t.Fatalf("readArtifactSet(valid) = %#v, %q, %v", artifacts, resolved, err)
	}
	if _, _, err := readArtifactSet(directory, []string{"different"}); err == nil {
		t.Fatal("readArtifactSet(mismatch) error = nil")
	}
	if err := revalidateArtifactSet(directory, artifacts, []string{"artifact"}); err != nil {
		t.Fatalf("revalidateArtifactSet(equal) error = %v", err)
	}
	writeFile(t, filepath.Join(directory, "artifact"), []byte("changed"))
	if err := revalidateArtifactSet(directory, artifacts, []string{"artifact"}); err == nil {
		t.Fatal("revalidateArtifactSet(changed) error = nil")
	}
}

func TestOwnedOutputWorkspaceAndVendorBoundaries(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	artifacts := map[string][]byte{"artifact": []byte("data")}
	for _, test := range []struct {
		name   string
		output string
	}{
		{name: "missing", output: ""},
		{name: "unsafe", output: filepath.Join(parent, "bad:name")},
		{name: "missing parent", output: filepath.Join(parent, "missing", "output")},
	} {
		if _, err := writeVerifiedOutput(test.output, artifacts, []string{"artifact"}); err == nil {
			t.Errorf("writeVerifiedOutput(%s) error = nil", test.name)
		}
	}
	root, rootErr := os.OpenRoot(parent)
	if rootErr != nil {
		t.Fatal(rootErr)
	}
	if err := writeVerifiedFile(root, "../escape", []byte("data")); err == nil {
		t.Error("writeVerifiedFile(escape) error = nil")
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := filesystemVendorFiles(filepath.Join(parent, "missing-vendor")); err == nil {
		t.Error("filesystemVendorFiles(missing) error = nil")
	}
	vendorFile := filepath.Join(parent, "vendor-file")
	writeFile(t, vendorFile, []byte("not a directory"))
	if _, err := filesystemVendorFiles(vendorFile); err == nil {
		t.Error("filesystemVendorFiles(file) error = nil")
	}
	vendorRoot := filepath.Join(parent, "vendor")
	if err := os.Mkdir(vendorRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(vendorRoot, "module.go"), []byte("package module\n"))
	writeFile(t, filepath.Join(vendorRoot, "other.go"), []byte("package module\n"))
	vendorFiles, vendorErr := filesystemVendorFiles(vendorRoot)
	if vendorErr != nil || string(vendorFiles["module.go"].content) != "package module\n" {
		t.Fatalf("filesystemVendorFiles(valid) = %#v, %v", vendorFiles, vendorErr)
	}
	vendorHandle, handleErr := os.OpenRoot(vendorRoot)
	if handleErr != nil {
		t.Fatal(handleErr)
	}
	otherInfo, infoErr := os.Lstat(filepath.Join(vendorRoot, "other.go"))
	if infoErr != nil {
		t.Fatal(infoErr)
	}
	if _, err := readVendorFile(vendorHandle, "missing", otherInfo); err == nil {
		t.Error("readVendorFile(missing) error = nil")
	}
	if _, err := readVendorFile(vendorHandle, "module.go", otherInfo); err == nil {
		t.Error("readVendorFile(changed identity) error = nil")
	}
	if err := vendorHandle.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := readWorkspaceFile(parent, "missing", maxControlFile); err == nil {
		t.Error("readWorkspaceFile(missing) error = nil")
	}
	if _, err := readWorkspaceFile(parent, "vendor", maxControlFile); err == nil {
		t.Error("readWorkspaceFile(directory) error = nil")
	}
	if _, err := readWorkspaceFile(parent, "vendor-file", 1); err == nil {
		t.Error("readWorkspaceFile(oversized) error = nil")
	}
	if err := (isolatedWorkspace{}).Close(); err != nil {
		t.Fatalf("empty isolatedWorkspace.Close() error = %v", err)
	}
}

func TestIsolatedWorkspaceCloseRemovesReadOnlyModuleCache(t *testing.T) {
	t.Parallel()
	workspaceRoot := filepath.Join(t.TempDir(), "workspace")
	moduleDirectory := filepath.Join(workspaceRoot, "module-cache", "golang.org", "x", "sync@v0.22.0")
	if err := os.MkdirAll(moduleDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	patents := filepath.Join(moduleDirectory, "PATENTS")
	writeFile(t, patents, []byte("license evidence\n"))
	if err := os.Chmod(patents, 0o400); err != nil {
		t.Fatalf("make %q read-only: %v", patents, err)
	}
	for _, path := range []string{moduleDirectory, filepath.Dir(moduleDirectory)} {
		if err := os.Chmod(path, 0o555); err != nil {
			t.Fatalf("make %q read-only: %v", path, err)
		}
	}
	workspace := isolatedWorkspace{base: workspaceRoot}
	if err := workspace.Close(); err != nil {
		t.Fatalf("Close(read-only module cache) error = %v", err)
	}
	if _, err := os.Lstat(workspaceRoot); !os.IsNotExist(err) {
		t.Fatalf("isolated workspace remains after Close: %v", err)
	}
}

func TestWorkspacePermissionRepairRestoresParentBeforeDescending(t *testing.T) {
	t.Parallel()
	workspaceRoot := filepath.Join(t.TempDir(), "workspace")
	nested := filepath.Join(workspaceRoot, "module-cache", "module@v1.0.0")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	moduleFile := filepath.Join(nested, "module.go")
	writeFile(t, moduleFile, []byte("package module\n"))
	if err := os.Chmod(moduleFile, 0o400); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{nested, filepath.Dir(nested), workspaceRoot} {
		if err := os.Chmod(path, 0o400); err != nil {
			t.Fatalf("remove owner search permission from %q: %v", path, err)
		}
	}
	if err := makeWorkspaceWritable(workspaceRoot); err != nil {
		t.Fatalf("makeWorkspaceWritable(no-search parent) error = %v", err)
	}
	if err := os.WriteFile(moduleFile, []byte("package repaired\n"), 0o600); err != nil {
		t.Fatalf("write repaired module file: %v", err)
	}
	if err := os.Mkdir(filepath.Join(nested, "child"), 0o700); err != nil {
		t.Fatalf("create child in repaired module directory: %v", err)
	}
	if err := (isolatedWorkspace{base: workspaceRoot}).Close(); err != nil {
		t.Fatalf("Close(repaired workspace) error = %v", err)
	}
}

func TestIsolatedWorkspaceCleanupRetriesAfterPermissionFailure(t *testing.T) {
	t.Parallel()
	workspaceRoot := filepath.Join(t.TempDir(), "workspace")
	if err := os.Mkdir(workspaceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	permissionErr := errors.New("read-only module cache")
	removeCalls := 0
	repairCalls := 0
	err := removeIsolatedWorkspace(workspaceRoot, workspaceCleanupOperations{
		removeAll: func(path string) error {
			removeCalls++
			if removeCalls == 1 {
				return permissionErr
			}
			return os.RemoveAll(path)
		},
		makeWritable: func(path string) error {
			repairCalls++
			return makeWorkspaceWritable(path)
		},
	})
	if err != nil {
		t.Fatalf("removeIsolatedWorkspace(transient permission failure) error = %v", err)
	}
	if removeCalls != 2 || repairCalls != 1 {
		t.Fatalf("cleanup calls = remove %d, repair %d", removeCalls, repairCalls)
	}
	if _, err := os.Lstat(workspaceRoot); !os.IsNotExist(err) {
		t.Fatalf("isolated workspace remains after retry: %v", err)
	}
}

func TestIsolatedWorkspaceCleanupReportsPersistentFailure(t *testing.T) {
	t.Parallel()
	initialFailure := errors.New("initial removal")
	repairFailure := errors.New("permission repair")
	retryFailure := errors.New("retry removal")
	removeCalls := 0
	err := removeIsolatedWorkspace("workspace", workspaceCleanupOperations{
		removeAll: func(string) error {
			removeCalls++
			if removeCalls == 1 {
				return initialFailure
			}
			return retryFailure
		},
		makeWritable: func(string) error { return repairFailure },
	})
	if !errors.Is(err, initialFailure) || !errors.Is(err, repairFailure) || !errors.Is(err, retryFailure) {
		t.Fatalf("removeIsolatedWorkspace(persistent failure) error = %v", err)
	}
	if removeCalls != 2 {
		t.Fatalf("cleanup remove calls = %d, want 2", removeCalls)
	}
}

func TestVerifiedOutputPublishNeverReplacesLateTarget(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	target := filepath.Join(parent, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(target, "owned-by-another-writer")
	writeFile(t, marker, []byte("preserve"))

	if _, err := writeVerifiedOutput(
		target,
		map[string][]byte{"artifact": []byte("verified")},
		[]string{"artifact"},
	); err == nil {
		t.Fatal("verified-output publication replaced an existing target")
	}
	content, err := os.ReadFile(marker)
	if err != nil || string(content) != "preserve" {
		t.Fatalf("late target marker = %q, %v", content, err)
	}
}

func TestArchiveMaterializationBoundaries(t *testing.T) {
	t.Parallel()
	policy := releasePolicies["spice-agent"]
	prefix := "spice-agent_0.1.0-preview.5/"
	source := sourceIdentity{entries: []gitEntry{{name: "README.md", mode: "100644"}}}
	rootEntry := archiveTestEntry{header: tar.Header{Name: prefix, Typeflag: tar.TypeDir, Mode: 0o775}}
	regular := archiveTestEntry{
		header:  tar.Header{Name: prefix + "README.md", Typeflag: tar.TypeReg, Mode: 0o664},
		content: []byte("fixture\n"),
	}
	for _, test := range []struct {
		name    string
		entries []archiveTestEntry
		want    string
	}{
		{name: "valid", entries: []archiveTestEntry{rootEntry, regular}},
		{name: "outside root", entries: []archiveTestEntry{{header: tar.Header{Name: "other/file", Typeflag: tar.TypeReg, Mode: 0o664}}}, want: "outside canonical root"},
		{name: "nonportable", entries: []archiveTestEntry{{header: tar.Header{Name: prefix + "bad\\name", Typeflag: tar.TypeReg, Mode: 0o664}}}, want: "contains traversal"},
		{name: "unexpected", entries: []archiveTestEntry{{header: tar.Header{Name: prefix + "other", Typeflag: tar.TypeReg, Mode: 0o664}}}, want: "unexpected file"},
		{name: "unsupported", entries: []archiveTestEntry{{header: tar.Header{Name: prefix + "README.md", Typeflag: tar.TypeSymlink, Linkname: "other"}}}, want: "unsupported type"},
		{name: "bad mode", entries: []archiveTestEntry{{header: tar.Header{Name: prefix + "README.md", Typeflag: tar.TypeReg, Mode: 0o600}}}, want: "invalid mode or size"},
		{name: "duplicate", entries: []archiveTestEntry{regular, regular}, want: "repeats file"},
		{name: "incomplete", entries: []archiveTestEntry{rootEntry}, want: "trusted Git tree contains"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := extractTrustedArchive(archiveFixture(t, test.entries), t.TempDir(), source, policy)
			if test.want == "" && err != nil {
				t.Fatalf("extractTrustedArchive() error = %v", err)
			}
			if test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) {
				t.Fatalf("extractTrustedArchive() error = %v, want %q", err, test.want)
			}
		})
	}
	if err := extractTrustedArchive([]byte("not gzip"), t.TempDir(), source, policy); err == nil {
		t.Error("extractTrustedArchive(invalid gzip) error = nil")
	}
	if _, err := materializeSourceArchive([]byte("not gzip"), source, policy); err == nil {
		t.Error("materializeSourceArchive(invalid gzip) error = nil")
	}
	nestedSource := sourceIdentity{entries: []gitEntry{{name: "nested/README.md", mode: "100644"}}}
	nestedRegular := regular
	nestedRegular.header.Name = prefix + "nested/README.md"
	if err := extractTrustedArchive(
		archiveFixture(t, []archiveTestEntry{rootEntry, nestedRegular}),
		t.TempDir(),
		nestedSource,
		policy,
	); err != nil {
		t.Fatalf("extractTrustedArchive(nested) error = %v", err)
	}
	archiveRoot, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := writeArchiveFile(archiveRoot, "short", 0o600, 2, strings.NewReader("x")); err == nil {
		t.Error("writeArchiveFile(short reader) error = nil")
	}
	if err := archiveRoot.Close(); err != nil {
		t.Fatal(err)
	}
}

func archiveFixture(t *testing.T, entries []archiveTestEntry) []byte {
	t.Helper()
	var result bytes.Buffer
	gzipWriter := gzip.NewWriter(&result)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, entry := range entries {
		header := entry.header
		header.Size = int64(len(entry.content))
		if err := tarWriter.WriteHeader(&header); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write(entry.content); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return result.Bytes()
}

func TestTrustedGitCommandAndRevalidationBoundaries(t *testing.T) {
	t.Parallel()
	fixture := newReleaseFixture(t, "")
	policy := releasePolicies[fixture.config.RepositoryName]
	source, sourceErr := trustedSource(t.Context(), fixture.config, policy)
	if sourceErr != nil {
		t.Fatalf("trustedSource() error = %v", sourceErr)
	}
	content, contentErr := readGitBlob(t.Context(), source, "README.md", maxControlFile)
	if contentErr != nil || string(content) != "# fixture\n" {
		t.Fatalf("readGitBlob(valid) = %q, %v", content, contentErr)
	}
	if _, err := readGitBlob(t.Context(), source, "missing", maxControlFile); err == nil {
		t.Fatal("readGitBlob(missing) error = nil")
	}
	executable := source
	executable.entries = []gitEntry{{name: "README.md", objectID: source.entries[0].objectID, mode: "100755"}}
	if _, err := readGitBlob(t.Context(), executable, "README.md", maxControlFile); err == nil {
		t.Fatal("readGitBlob(executable) error = nil")
	}
	if err := revalidateSource(t.Context(), source, moduleFixtureVersion); err != nil {
		t.Fatalf("revalidateSource(valid) error = %v", err)
	}

	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := expectedSourceArchive(canceled, source, policy.repository, policy.version); err == nil {
		t.Fatal("expectedSourceArchive(canceled) error = nil")
	}
	runner, runnerErr := newSystemGoRunner()
	if runnerErr != nil {
		t.Fatalf("newSystemGoRunner() error = %v", runnerErr)
	}
	if _, err := runner.Output(
		t.Context(), fixture.root, os.Environ(), "not-a-go-command",
	); err == nil {
		t.Fatal("Go runner(invalid command) error = nil")
	}
	if _, err := gitOutput(t.Context(), fixture.root, maxDiagnostic, "not-a-git-command"); err == nil {
		t.Fatal("gitOutput(invalid command) error = nil")
	}
	if _, err := gitOutput(t.Context(), fixture.root, 1, "rev-parse", "HEAD"); err == nil ||
		!strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("gitOutput(truncated) error = %v", err)
	}

	runGit(t, fixture.root, "tag", "-d", moduleFixtureVersion)
	if err := revalidateSource(t.Context(), source, moduleFixtureVersion); err == nil {
		t.Fatal("revalidateSource(deleted tag) error = nil")
	}
}
