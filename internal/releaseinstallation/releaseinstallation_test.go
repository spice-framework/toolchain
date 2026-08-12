package releaseinstallation

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const fixtureCommit = "0123456789abcdef0123456789abcdef01234567"

func TestVerifyAndExtractValidatedToolchainSubjects(t *testing.T) {
	t.Parallel()
	directory := releaseFixture(t)
	set, err := Verify(directory)
	if err != nil {
		t.Fatal(err)
	}
	if set.Version() != releaseVersion || set.Commit() != fixtureCommit {
		t.Fatalf("verified identity = %q %q", set.Version(), set.Commit())
	}
	for _, target := range []targetExpectation{{goos: "linux", goarch: "amd64"}, {goos: "windows", goarch: "amd64"}} {
		destination := filepath.Join(t.TempDir(), "Toolchain π installed "+target.goos)
		root, extractErr := set.ExtractNative(destination, target.goos, target.goarch)
		if extractErr != nil {
			t.Fatalf("extract %s: %v", target.goos, extractErr)
		}
		binary := "spice"
		if target.goos == "windows" {
			binary += ".exe"
		}
		content, readErr := os.ReadFile(filepath.Join(root, binary)) // #nosec G304 -- exact fixture member.
		if readErr != nil || string(content) != "binary:"+binary {
			t.Fatalf("extracted %s = %q, %v", binary, content, readErr)
		}
		for name, want := range map[string]string{"LICENSE": "license", "README.md": "readme"} {
			content, readErr = os.ReadFile(filepath.Join(root, name)) // #nosec G304 -- exact fixture member.
			if readErr != nil || string(content) != want {
				t.Fatalf("extracted %s = %q, %v", name, content, readErr)
			}
		}
	}
	if _, err = set.ExtractNative("relative", "linux", "amd64"); err == nil {
		t.Fatal("relative extraction path was accepted")
	}
	if _, err = set.ExtractNative(t.TempDir(), "linux", "amd64"); err == nil {
		t.Fatal("existing extraction path was accepted")
	}
	if _, err = set.ExtractNative(filepath.Join(t.TempDir(), "new"), "plan9", "amd64"); err == nil {
		t.Fatal("unsupported target was accepted")
	}
	archive := set.archives["linux/amd64"]
	writeTestFile(t, archive, []byte("changed after validation"))
	failed := filepath.Join(t.TempDir(), "failed extraction")
	if _, err = set.ExtractNative(failed, "linux", "amd64"); err == nil || !strings.Contains(err.Error(), "revalidate") {
		t.Fatal("changed archive was extracted")
	}
	if _, statErr := os.Stat(failed); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("failed extraction destination remains: %v", statErr)
	}
	var unavailable *Set
	if unavailable.Version() != "" || unavailable.Commit() != "" {
		t.Fatal("nil set exposed identity")
	}
	if _, err = unavailable.ExtractNative(filepath.Join(t.TempDir(), "new"), "linux", "amd64"); err == nil {
		t.Fatal("nil set extracted an archive")
	}
}

func TestVerifyRejectsSubjectAndChecksumDrift(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		mutate func(*testing.T, string)
	}{
		{name: "missing subject", mutate: func(t *testing.T, directory string) {
			t.Helper()
			if err := os.Remove(filepath.Join(directory, "checksums.txt")); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "unexpected subject", mutate: func(t *testing.T, directory string) {
			t.Helper()
			writeTestFile(t, filepath.Join(directory, "unexpected"), []byte("unexpected"))
		}},
		{name: "non regular subject", mutate: func(t *testing.T, directory string) {
			t.Helper()
			if err := os.Remove(filepath.Join(directory, sbomName())); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(filepath.Join(directory, sbomName()), 0o700); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "subject hash", mutate: func(t *testing.T, directory string) {
			t.Helper()
			writeTestFile(t, filepath.Join(directory, sbomName()), []byte("{}\n"))
		}},
		{name: "partial checksums", mutate: func(t *testing.T, directory string) {
			t.Helper()
			file := filepath.Join(directory, "checksums.txt")
			content, err := os.ReadFile(file) // #nosec G304 -- exact fixture path.
			if err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, file, content[:bytes.IndexByte(content, '\n')+1])
		}},
		{name: "noncanonical checksums", mutate: func(t *testing.T, directory string) {
			t.Helper()
			file := filepath.Join(directory, "checksums.txt")
			content, err := os.ReadFile(file) // #nosec G304 -- exact fixture path.
			if err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, file, bytes.Replace(content, []byte("  "), []byte(" "), 1))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			directory := releaseFixture(t)
			test.mutate(t, directory)
			if _, err := Verify(directory); err == nil {
				t.Fatal("Verify() error = nil")
			}
		})
	}
	if _, err := Verify("relative"); err == nil {
		t.Fatal("relative artifact directory was accepted")
	}
}

func TestReleaseMetadataAndSBOMRejectDrift(t *testing.T) {
	t.Parallel()
	valid := fixtureMetadata(t, t.TempDir())
	if err := validateReleaseMetadata(valid); err != nil {
		t.Fatal(err)
	}
	mutations := []func(*releaseMetadata){
		func(value *releaseMetadata) { value.Schema = 2 },
		func(value *releaseMetadata) { value.Version = "v0.1.0-preview.2" },
		func(value *releaseMetadata) { value.Version = "v0.1.0-preview.3" },
		func(value *releaseMetadata) { value.Commit = "not-a-commit" },
		func(value *releaseMetadata) { value.Build.CGOEnabled = true },
		func(value *releaseMetadata) { value.Build.Identity.VersionSymbol = "example.com/wrong.Version" },
		func(value *releaseMetadata) { value.Build.Identity.CommitValue = strings.Repeat("0", 40) },
		func(value *releaseMetadata) { value.Targets = value.Targets[:5] },
		func(value *releaseMetadata) { value.Targets[0].Archive = "wrong.tar.gz" },
		func(value *releaseMetadata) { value.Payloads = value.Payloads[:1] },
		func(value *releaseMetadata) { value.Payloads[0].SHA256 = "bad" },
	}
	for index, mutate := range mutations {
		candidate := valid
		candidate.Targets = slices.Clone(valid.Targets)
		candidate.Payloads = slices.Clone(valid.Payloads)
		mutate(&candidate)
		if err := validateReleaseMetadata(candidate); err == nil {
			t.Fatalf("metadata mutation %d was accepted", index)
		}
	}
	directory := releaseFixture(t)
	metadataFile := filepath.Join(directory, releaseMetadataName())
	content, err := os.ReadFile(metadataFile) // #nosec G304 -- exact fixture path.
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, metadataFile, bytes.ReplaceAll(content, []byte("  "), []byte(" ")))
	writeChecksums(t, directory, expectedSubjectNames()[1:])
	if _, err = Verify(directory); err == nil {
		t.Fatal("noncanonical release metadata was accepted")
	}
	sbom := filepath.Join(t.TempDir(), "sbom.json")
	writeTestFile(t, sbom, []byte(`{"spdxVersion":"SPDX-2.2"}`))
	if err = validateSBOM(context.Background(), sbom); err == nil {
		t.Fatal("invalid SPDX identity was accepted")
	}
}

func TestArchiveValidationRejectsZipDrift(t *testing.T) {
	t.Parallel()
	payloads := []releaseFile{
		{Name: "LICENSE", SHA256: digestText("license"), Size: int64(len("license"))},
		{Name: "README.md", SHA256: digestText("readme"), Size: int64(len("readme"))},
	}
	target := releaseTarget{
		GOOS: "windows", GOARCH: "amd64", Archive: "toolchain_0.1.0-preview.7_windows_amd64.zip",
		Binaries: []string{"spice.exe"},
	}
	valid := []archiveFixtureEntry{
		{name: "LICENSE", mode: 0o644, content: "license"},
		{name: "README.md", mode: 0o644, content: "readme"},
		{name: "spice.exe", mode: 0o755, content: "binary"},
	}
	for _, test := range []struct {
		name    string
		entries []archiveFixtureEntry
	}{
		{name: "valid", entries: valid},
		{name: "encrypted", entries: replaceArchiveEntry(valid, "spice.exe", archiveFixtureEntry{name: "spice.exe", mode: 0o755, content: "binary", encrypted: true})},
		{name: "traversal", entries: []archiveFixtureEntry{{name: "../escape", mode: 0o644, content: "license"}}},
		{name: "wrong mode", entries: replaceArchiveEntry(valid, "spice.exe", archiveFixtureEntry{name: "spice.exe", mode: 0o644, content: "binary"})},
		{name: "duplicate", entries: append(slices.Clone(valid), valid[0])},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			file := filepath.Join(t.TempDir(), target.Archive)
			writeZipArchive(t, file, archiveRoot(target.Archive), test.entries)
			err := inspectArchive(file, target, payloads, nil)
			if test.name == "valid" && err != nil {
				t.Fatal(err)
			}
			if test.name != "valid" && err == nil {
				t.Fatal("inspectArchive() error = nil")
			}
		})
	}
}

func TestReleaseSubjectIOHonorsCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	reader := readerWithContext(ctx, &cancelingReader{cancel: cancel})
	if _, err := io.ReadAll(reader); !errors.Is(err, context.Canceled) {
		t.Fatalf("mid-stream cancellation error = %v", err)
	}
	canceled, stop := context.WithCancel(context.Background())
	stop()
	if _, err := VerifyContext(canceled, canonicalFixtureDirectory(t)); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled VerifyContext() error = %v", err)
	}
	if _, err := VerifyContext(nil, canonicalFixtureDirectory(t)); err == nil { //nolint:staticcheck // Proves the public nil-context boundary fails closed.
		t.Fatal("nil verification context was accepted")
	}
}

func TestArchiveValidationRejectsPathModePayloadAndMembershipDrift(t *testing.T) {
	t.Parallel()
	payloads := []releaseFile{
		{Name: "LICENSE", SHA256: digestText("license"), Size: int64(len("license"))},
		{Name: "README.md", SHA256: digestText("readme"), Size: int64(len("readme"))},
	}
	target := releaseTarget{
		GOOS: "linux", GOARCH: "amd64", Archive: "toolchain_0.1.0-preview.7_linux_amd64.tar.gz",
		Binaries: []string{"spice"},
	}
	valid := []archiveFixtureEntry{
		{name: "LICENSE", mode: 0o644, content: "license"},
		{name: "README.md", mode: 0o644, content: "readme"},
		{name: "spice", mode: 0o755, content: "binary"},
	}
	for _, test := range []struct {
		name    string
		entries []archiveFixtureEntry
	}{
		{name: "valid", entries: valid},
		{name: "traversal", entries: []archiveFixtureEntry{{name: "../escape", mode: 0o644, content: "license"}}},
		{name: "wrong mode", entries: replaceArchiveEntry(valid, "spice", archiveFixtureEntry{name: "spice", mode: 0o644, content: "binary"})},
		{name: "payload digest", entries: replaceArchiveEntry(valid, "LICENSE", archiveFixtureEntry{name: "LICENSE", mode: 0o644, content: "changed"})},
		{name: "missing binary", entries: valid[:2]},
		{name: "duplicate", entries: append(slices.Clone(valid), valid[0])},
		{name: "undeclared", entries: append(slices.Clone(valid), archiveFixtureEntry{name: "other", mode: 0o644, content: "other"})},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			file := filepath.Join(t.TempDir(), target.Archive)
			writeTarArchive(t, file, archiveRoot(target.Archive), test.entries)
			err := inspectArchive(file, target, payloads, nil)
			if test.name == "valid" && err != nil {
				t.Fatal(err)
			}
			if test.name != "valid" && err == nil {
				t.Fatal("inspectArchive() error = nil")
			}
		})
	}
}

func replaceArchiveEntry(entries []archiveFixtureEntry, name string, replacement archiveFixtureEntry) []archiveFixtureEntry {
	result := slices.Clone(entries)
	for index := range result {
		if result[index].name == name {
			result[index] = replacement
			break
		}
	}
	return result
}

type archiveFixtureEntry struct {
	name, content string
	mode          os.FileMode
	encrypted     bool
}

type cancelingReader struct {
	cancel context.CancelFunc
}

func (reader *cancelingReader) Read(buffer []byte) (int, error) {
	for index := range buffer {
		buffer[index] = 'x'
	}
	reader.cancel()
	return len(buffer), nil
}

func releaseFixture(t *testing.T) string {
	t.Helper()
	directory := t.TempDir()
	metadata := fixtureMetadata(t, directory)
	content, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(directory, releaseMetadataName()), append(content, '\n'))
	writeChecksums(t, directory, expectedSubjectNames()[1:])
	return directory
}

func fixtureMetadata(t *testing.T, directory string) releaseMetadata {
	t.Helper()
	payloadContents := map[string]string{"LICENSE": "license", "README.md": "readme"}
	payloads := make([]releaseFile, 0, len(expectedPayloadNames))
	for _, name := range expectedPayloadNames {
		content := payloadContents[name]
		payloads = append(payloads, releaseFile{Name: name, SHA256: digestText(content), Size: int64(len(content))})
	}
	targets := make([]releaseTarget, 0, len(supportedTargets))
	artifacts := make([]releaseFile, 0, len(supportedTargets)+1)
	for _, current := range supportedTargets {
		extension := ".tar.gz"
		binary := "spice"
		if current.goos == "windows" {
			extension = ".zip"
			binary += ".exe"
		}
		archive := artifactBase() + "_" + current.goos + "_" + current.goarch + extension
		target := releaseTarget{GOOS: current.goos, GOARCH: current.goarch, Archive: archive, Binaries: []string{binary}}
		targets = append(targets, target)
		entries := []archiveFixtureEntry{
			{name: "LICENSE", mode: 0o644, content: "license"},
			{name: "README.md", mode: 0o644, content: "readme"},
			{name: binary, mode: 0o755, content: "binary:" + binary},
		}
		file := filepath.Join(directory, archive)
		if current.goos == "windows" {
			writeZipArchive(t, file, archiveRoot(archive), entries)
		} else {
			writeTarArchive(t, file, archiveRoot(archive), entries)
		}
		artifacts = append(artifacts, fileMetadata(t, file))
	}
	sbomContent := []byte(`{"spdxVersion":"SPDX-2.3","dataLicense":"CC0-1.0","SPDXID":"SPDXRef-DOCUMENT","name":"toolchain v0.1.0-preview.7","documentNamespace":"https://github.com/spice-framework/toolchain/releases/v0.1.0-preview.7/spdx/test","packages":[{"name":"github.com/spice-framework/toolchain","versionInfo":"v0.1.0-preview.7"}]}` + "\n")
	writeTestFile(t, filepath.Join(directory, sbomName()), sbomContent)
	artifacts = append(artifacts, fileMetadata(t, filepath.Join(directory, sbomName())))
	slices.SortFunc(artifacts, func(left, right releaseFile) int { return strings.Compare(left.Name, right.Name) })
	return releaseMetadata{
		Schema: 1, Profile: releaseProfile, Repository: releaseRepository,
		Module: releaseModule, Source: "https://github.com/spice-framework/toolchain",
		Version: releaseVersion, Commit: fixtureCommit, SourceDateEpoch: 1,
		Go: "1.26.5", Toolchain: "go1.26.5",
		Build: releaseBuild{
			ModuleMode: "vendor", Trimpath: true, Environment: "closed", CacheIsolation: true,
			Source: "materialized-tagged-commit", GOAMD64: "v1", GOARM64: "v8.0",
			Identity: releaseIdentity{
				VersionSymbol: releaseModule + "/internal/cli.Version",
				VersionValue:  strings.TrimPrefix(releaseVersion, "v"),
				CommitSymbol:  releaseModule + "/internal/cli.Commit",
				CommitValue:   fixtureCommit,
			},
		},
		Targets: targets, Payloads: payloads, Artifacts: artifacts,
	}
}

func writeTarArchive(t *testing.T, file, root string, entries []archiveFixtureEntry) {
	t.Helper()
	opened, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	compressed := gzip.NewWriter(opened)
	archive := tar.NewWriter(compressed)
	for _, entry := range entries {
		content := []byte(entry.content)
		header := &tar.Header{Name: root + "/" + entry.name, Mode: int64(entry.mode), Size: int64(len(content))}
		if err = archive.WriteHeader(header); err == nil {
			_, err = archive.Write(content)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if err = errors.Join(archive.Close(), compressed.Close(), opened.Close()); err != nil {
		t.Fatal(err)
	}
}

func writeZipArchive(t *testing.T, file, root string, entries []archiveFixtureEntry) {
	t.Helper()
	opened, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(opened)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: root + "/" + entry.name, Method: zip.Deflate}
		header.SetMode(entry.mode)
		if entry.encrypted {
			header.Flags |= 1
		}
		writer, createErr := archive.CreateHeader(header)
		if createErr == nil {
			_, createErr = io.WriteString(writer, entry.content)
		}
		if createErr != nil {
			t.Fatal(createErr)
		}
	}
	if err = errors.Join(archive.Close(), opened.Close()); err != nil {
		t.Fatal(err)
	}
}

func fileMetadata(t *testing.T, file string) releaseFile {
	t.Helper()
	content, err := os.ReadFile(file) // #nosec G304 -- exact fixture file.
	if err != nil {
		t.Fatal(err)
	}
	return releaseFile{Name: filepath.Base(file), SHA256: digestBytes(content), Size: int64(len(content))}
}

func writeChecksums(t *testing.T, directory string, names []string) {
	t.Helper()
	var content strings.Builder
	for _, name := range names {
		file, err := os.ReadFile(filepath.Join(directory, name)) // #nosec G304 -- exact fixture member.
		if err != nil {
			t.Fatal(err)
		}
		content.WriteString(digestBytes(file) + "  " + name + "\n")
	}
	writeTestFile(t, filepath.Join(directory, "checksums.txt"), []byte(content.String()))
}

func writeTestFile(t *testing.T, file string, content []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func digestText(value string) string { return digestBytes([]byte(value)) }

func digestBytes(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func canonicalFixtureDirectory(t *testing.T) string {
	t.Helper()
	directory, err := filepath.Abs(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(directory)
}
