package releaseverify

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const testVersion = "v0.1.0"

type releaseFixture struct {
	directory  string
	repository string
	commit     string
	epoch      time.Time
	privateKey ed25519.PrivateKey
	publicPEM  []byte
	source     []archiveEntry
	linuxAMD64 []byte
}

func TestVerifyCompleteAndAdversarialRelease(t *testing.T) {
	fixture := newReleaseFixture(t)
	result, err := Verify(t.Context(), fixture.config())
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if result.Commit != fixture.commit || !result.Epoch.Equal(fixture.epoch) ||
		len(result.Files) != 11 {
		t.Fatalf("Verify() = %#v", result)
	}
	tests := []struct {
		name   string
		mutate func(*testing.T, string)
		config func(*testing.T, Config) Config
		want   string
	}{
		{
			name: "extra asset",
			mutate: func(t *testing.T, directory string) {
				t.Helper()
				writeTestFile(t, filepath.Join(directory, "unexpected.txt"), []byte("x"))
			},
			want: "artifact set",
		},
		{
			name: "wrong trusted key",
			config: func(t *testing.T, config Config) Config {
				t.Helper()
				_, key, err := ed25519.GenerateKey(rand.Reader)
				if err != nil {
					t.Fatal(err)
				}
				publicKey, ok := key.Public().(ed25519.PublicKey)
				if !ok {
					t.Fatal("generated private key has unexpected public-key type")
				}
				config.TrustedPublicKey = encodeTestPublicKey(t, publicKey)
				return config
			},
			want: "does not match trusted",
		},
		{
			name: "tampered signature",
			mutate: func(t *testing.T, directory string) {
				t.Helper()
				signature := readTestFile(t, filepath.Join(directory, "checksums.txt.sig"))
				signature[0] ^= 0xff
				writeExistingTestFile(t, filepath.Join(directory, "checksums.txt.sig"), signature)
			},
			want: "signature is invalid",
		},
		{
			name: "noncanonical checksums",
			mutate: func(t *testing.T, directory string) {
				t.Helper()
				data := readTestFile(t, filepath.Join(directory, "checksums.txt"))
				lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
				slices.Reverse(lines)
				writeExistingTestFile(
					t,
					filepath.Join(directory, "checksums.txt"),
					[]byte(strings.Join(lines, "\n")+"\n"),
				)
				signCurrentChecksums(t, directory, fixture.privateKey, fixture.publicPEM)
			},
			want: "not canonical",
		},
		{
			name: "source differs from commit",
			mutate: func(t *testing.T, directory string) {
				t.Helper()
				entries := cloneArchiveEntries(fixture.source)
				for index := range entries {
					if strings.HasSuffix(entries[index].name, "/README.md") {
						entries[index].data = []byte("forged\n")
					}
				}
				name := "spice_0.1.0_source.tar.gz"
				writeTestTarGzip(t, filepath.Join(directory, name), fixture.epoch, entries)
				rebuildChecksums(t, directory, fixture.privateKey, fixture.publicPEM)
			},
			want: "does not match trusted path",
		},
		{
			name: "wrong archive mode",
			mutate: func(t *testing.T, directory string) {
				t.Helper()
				name := "spice_0.1.0_linux_amd64.tar.gz"
				entries := readTarForTest(t, filepath.Join(directory, name))
				entries[0].mode = 0o644
				writeTestTarGzip(t, filepath.Join(directory, name), fixture.epoch, entries)
				rebuildChecksums(t, directory, fixture.privateKey, fixture.publicPEM)
			},
			want: "unexpected path or mode",
		},
		{
			name: "SBOM graph drift",
			mutate: func(t *testing.T, directory string) {
				t.Helper()
				name := "spice_0.1.0_sbom.spdx.json"
				data := readTestFile(t, filepath.Join(directory, name))
				data = []byte(strings.Replace(string(data), "Spice v0.1.0", "Forged v0.1.0", 1))
				writeExistingTestFile(t, filepath.Join(directory, name), data)
				rebuildChecksums(t, directory, fixture.privateKey, fixture.publicPEM)
			},
			want: "does not exactly match",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := copyArtifactDirectory(t, fixture.directory)
			if test.mutate != nil {
				test.mutate(t, directory)
			}
			config := fixture.config()
			config.Directory = directory
			if test.config != nil {
				config = test.config(t, config)
			}
			_, err := Verify(t.Context(), config)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Verify() error = %v, want containing %q", err, test.want)
			}
		})
	}
	target := releaseTarget{goos: "linux", goarch: "amd64"}
	dependencies := map[string]string{"example.com/untrusted": "v1.0.0"}
	if err := verifyBuildInfo(fixture.linuxAMD64, target, testVersion, dependencies); err == nil ||
		!strings.Contains(err.Error(), "dependency graph") {
		t.Fatalf("verifyBuildInfo(dependency drift) error = %v", err)
	}
	vcsBinary := buildTestBinary(t, fixture.repository, target, testVersion, true)
	if err := verifyBuildInfo(
		vcsBinary,
		target,
		testVersion,
		map[string]string{},
	); err == nil || !strings.Contains(err.Error(), "unexpected build settings") {
		t.Fatalf("verifyBuildInfo(VCS metadata) error = %v", err)
	}
}

func TestVerifyRejectsCanceledContextAndSymbolicCommit(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Verify(ctx, Config{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("Verify(canceled) error = %v", err)
	}
	config := Config{Directory: ".", Repository: ".", Version: testVersion, Commit: "HEAD"}
	if _, err := Verify(t.Context(), config); err == nil || !strings.Contains(err.Error(), "exact") {
		t.Fatalf("Verify(symbolic commit) error = %v", err)
	}
	config.Commit = strings.Repeat("0", 40)
	config.Version = "v0.1"
	if _, err := Verify(t.Context(), config); err == nil || !strings.Contains(err.Error(), "canonical") {
		t.Fatalf("Verify(noncanonical version) error = %v", err)
	}
	config.Version = testVersion
	config.TrustedPublicKey = bytes.Repeat([]byte{'x'}, ed25519.PublicKeySize)
	if _, err := Verify(t.Context(), config); err == nil || !strings.Contains(err.Error(), "PEM") {
		t.Fatalf("Verify(raw trust anchor) error = %v", err)
	}
}

func TestValidateArchivePathRejectsUnsafeNames(t *testing.T) {
	t.Parallel()
	for _, name := range []string{
		"", ".", "../spice", "folder/../../spice", "/spice", `C:/spice`, `folder\spice`,
	} {
		if err := validateArchivePath(name); err == nil {
			t.Errorf("validateArchivePath(%q) succeeded", name)
		}
	}
	if err := validateArchivePath("spice-0.1.0/cmd/spice/main.go"); err != nil {
		t.Fatalf("validateArchivePath(valid) error = %v", err)
	}
}

func TestVerifyBuildInfoMatchesToolchainModuleGraph(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	target := releaseTarget{goos: "linux", goarch: "amd64"}
	modules, err := listBinaryModules(t.Context(), root, target)
	if err != nil {
		t.Fatalf("listBinaryModules() error = %v", err)
	}
	binary := buildTestBinary(t, root, target, testVersion, false)
	if err := verifyBuildInfo(binary, target, testVersion, modules); err != nil {
		t.Fatalf("verifyBuildInfo(toolchain) error = %v", err)
	}
}

func TestAuthenticatedArtifactBytesRemainStableAfterPathReplacement(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	name := "artifact.bin"
	original := []byte("signed bytes")
	writeTestFile(t, filepath.Join(directory, name), original)
	digest := sha256.Sum256(original)
	authenticated, err := readAuthenticatedArtifact(t.Context(), directory, name, digest)
	if err != nil {
		t.Fatalf("readAuthenticatedArtifact() error = %v", err)
	}
	writeExistingTestFile(t, filepath.Join(directory, name), []byte("replacement"))
	if string(authenticated) != string(original) {
		t.Fatalf("authenticated bytes changed to %q", authenticated)
	}
	if _, err := readAuthenticatedArtifact(t.Context(), directory, name, digest); err == nil ||
		!strings.Contains(err.Error(), "SHA-256 mismatch") {
		t.Fatalf("readAuthenticatedArtifact(replaced) error = %v", err)
	}
}

func TestBoundedOutputRecordsOverflow(t *testing.T) {
	t.Parallel()
	var output boundedOutput
	data := bytes.Repeat([]byte{'x'}, maxExecutedOutputBytes+1)
	count, err := output.Write(data)
	if err != nil || count != len(data) || !output.overflow ||
		output.data.Len() != maxExecutedOutputBytes {
		t.Fatalf(
			"Write() = %d, %v; overflow %t, retained %d",
			count,
			err,
			output.overflow,
			output.data.Len(),
		)
	}
}

func TestGitOutputRejectsMissingArguments(t *testing.T) {
	t.Parallel()
	if _, err := gitOutput(t.Context(), t.TempDir()); err == nil ||
		!strings.Contains(err.Error(), "no arguments") {
		t.Fatalf("gitOutput() error = %v", err)
	}
}

func (fixture releaseFixture) config() Config {
	return Config{
		Directory:        fixture.directory,
		Version:          testVersion,
		Repository:       fixture.repository,
		Commit:           fixture.commit,
		TrustedPublicKey: fixture.publicPEM,
	}
}

func newReleaseFixture(t *testing.T) releaseFixture {
	t.Helper()
	repository := t.TempDir()
	epoch := time.Date(2026, time.August, 5, 12, 34, 56, 0, time.UTC)
	files := map[string][]byte{
		"LICENSE":                 []byte("test license\n"),
		"README.md":               []byte("# Test Spice\n"),
		"cmd/spice/main.go":       []byte("package main\n\nimport (\n\t\"fmt\"\n\t\"github.com/spice-framework/toolchain/internal/cli\"\n)\n\nfunc main() { fmt.Printf(\"spice %s\\n\", cli.Version) }\n"),
		"go.mod":                  []byte("module github.com/spice-framework/toolchain\n\ngo 1.26.0\n\ntoolchain go1.26.5\n"),
		"internal/cli/version.go": []byte("package cli\n\nvar Version = \"development\"\n"),
		"vendor/modules.txt":      nil,
	}
	for name, data := range files {
		filename := filepath.Join(repository, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(filename), 0o750); err != nil {
			t.Fatal(err)
		}
		writeTestFile(t, filename, data)
	}
	gitTest(t, repository, "init")
	gitTest(t, repository, "config", "user.email", "release@example.invalid")
	gitTest(t, repository, "config", "user.name", "Release Test")
	gitTest(t, repository, "add", ".")
	linkBlob := gitTestInput(t, repository, "README.md", "hash-object", "-w", "--stdin")
	gitTest(
		t,
		repository,
		"update-index",
		"--add",
		"--cacheinfo",
		"120000,"+strings.TrimSpace(linkBlob)+",README-link",
	)
	command := exec.Command("git", "commit", "-m", "fixture")
	command.Dir = repository
	date := epoch.Format(time.RFC3339)
	command.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v: %s", err, output)
	}
	commit := strings.TrimSpace(gitTest(t, repository, "rev-parse", "HEAD"))
	directory := t.TempDir()
	versionName := strings.TrimPrefix(testVersion, "v")
	var linuxAMD64 []byte
	for _, target := range releaseTargets() {
		binary := buildTestBinary(t, repository, target, testVersion, false)
		if target.goos == "linux" && target.goarch == "amd64" {
			linuxAMD64 = append([]byte(nil), binary...)
		}
		name := target.archiveName(versionName)
		base := strings.TrimSuffix(name, map[bool]string{true: ".zip", false: ".tar.gz"}[target.windows])
		entries := []archiveEntry{
			{name: filepath.ToSlash(filepath.Join(base, target.executableName())), mode: 0o755, data: binary},
			{name: filepath.ToSlash(filepath.Join(base, "LICENSE")), mode: 0o644, data: files["LICENSE"]},
			{name: filepath.ToSlash(filepath.Join(base, "README.md")), mode: 0o644, data: files["README.md"]},
		}
		filename := filepath.Join(directory, name)
		if target.windows {
			writeTestZip(t, filename, epoch, entries)
		} else {
			writeTestTarGzip(t, filename, epoch, entries)
		}
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	source := make([]archiveEntry, 0, len(names))
	for _, name := range names {
		source = append(source, archiveEntry{
			name: "spice-" + versionName + "/" + name,
			mode: 0o644,
			data: files[name],
		})
	}
	source = append(source, archiveEntry{
		name: "spice-" + versionName + "/README-link", mode: 0o777, linkTarget: "README.md",
	})
	slices.SortFunc(source, func(left, right archiveEntry) int {
		return strings.Compare(left.name, right.name)
	})
	writeTestTarGzip(
		t,
		filepath.Join(directory, "spice_"+versionName+"_source.tar.gz"),
		epoch,
		source,
	)
	sbom := expectedSBOM([]listedModule{{path: modulePath, main: true}}, testVersion, epoch)
	sbomData, err := json.MarshalIndent(sbom, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(
		t,
		filepath.Join(directory, "spice_"+versionName+"_sbom.spdx.json"),
		append(sbomData, '\n'),
	)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicPEM := encodeTestPublicKey(t, publicKey)
	rebuildChecksums(t, directory, privateKey, publicPEM)
	return releaseFixture{
		directory: directory, repository: repository, commit: commit, epoch: epoch,
		privateKey: privateKey, publicPEM: publicPEM, source: source,
		linuxAMD64: linuxAMD64,
	}
}

func encodeTestPublicKey(t *testing.T, publicKey ed25519.PublicKey) []byte {
	t.Helper()
	encoded, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: encoded})
}

func buildTestBinary(
	t *testing.T,
	repository string,
	target releaseTarget,
	version string,
	withVCS bool,
) []byte {
	t.Helper()
	output := filepath.Join(t.TempDir(), "spice")
	buildVCS := "-buildvcs=false"
	if withVCS {
		buildVCS = "-buildvcs=true"
	}
	command := exec.Command(
		"go", "build", "-mod=vendor", "-trimpath", buildVCS,
		"-ldflags=-s -w -X "+modulePath+"/internal/cli.Version="+version,
		"-o", output, "./cmd/spice",
	)
	command.Dir = repository
	command.Env = append(
		os.Environ(),
		"CGO_ENABLED=0", "GOOS="+target.goos, "GOARCH="+target.goarch,
		"GOAMD64=v1", "GOARM64=v8.0", "GOTOOLCHAIN=local", "GOWORK=off",
	)
	if combined, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build %s/%s fixture: %v: %s", target.goos, target.goarch, err, combined)
	}
	return readTestFile(t, output)
}

func rebuildChecksums(
	t *testing.T,
	directory string,
	privateKey ed25519.PrivateKey,
	publicPEM []byte,
) {
	t.Helper()
	_, names := expectedAssetNames(testVersion)
	var checksums strings.Builder
	for _, name := range names {
		data := readTestFile(t, filepath.Join(directory, name))
		sum := sha256.Sum256(data)
		fmt.Fprintf(&checksums, "%s  %s\n", hex.EncodeToString(sum[:]), name)
	}
	writeExistingOrNewTestFile(t, filepath.Join(directory, "checksums.txt"), []byte(checksums.String()))
	signCurrentChecksums(t, directory, privateKey, publicPEM)
}

func signCurrentChecksums(
	t *testing.T,
	directory string,
	privateKey ed25519.PrivateKey,
	publicPEM []byte,
) {
	t.Helper()
	checksums := readTestFile(t, filepath.Join(directory, "checksums.txt"))
	writeExistingOrNewTestFile(
		t,
		filepath.Join(directory, "checksums.txt.sig"),
		ed25519.Sign(privateKey, checksums),
	)
	writeExistingOrNewTestFile(t, filepath.Join(directory, "checksums.txt.pem"), publicPEM)
}

func writeTestTarGzip(
	t *testing.T,
	filename string,
	epoch time.Time,
	entries []archiveEntry,
) {
	t.Helper()
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter, err := gzip.NewWriterLevel(file, gzip.BestCompression)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter.ModTime = epoch
	gzipWriter.OS = 255
	tarWriter := tar.NewWriter(gzipWriter)
	for _, entry := range entries {
		header := &tar.Header{
			Name: entry.name, Mode: int64(entry.mode), Size: int64(len(entry.data)),
			ModTime: epoch, AccessTime: epoch, ChangeTime: epoch, Format: tar.FormatPAX,
			Typeflag: tar.TypeReg,
		}
		if entry.linkTarget != "" {
			header.Typeflag = tar.TypeSymlink
			header.Size = 0
			header.Linkname = entry.linkTarget
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if entry.linkTarget == "" {
			if _, err := tarWriter.Write(entry.data); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeTestZip(t *testing.T, filename string, epoch time.Time, entries []archiveEntry) {
	t.Helper()
	file, err := os.Create(filename)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate, Modified: epoch}
		header.SetMode(entry.mode)
		target, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := target.Write(entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func readTarForTest(t *testing.T, filename string) []archiveEntry {
	t.Helper()
	file, err := os.Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			t.Errorf("close test archive: %v", closeErr)
		}
	}()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := gzipReader.Close(); closeErr != nil {
			t.Errorf("close test gzip: %v", closeErr)
		}
	}()
	reader := tar.NewReader(gzipReader)
	var entries []archiveEntry
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, archiveEntry{
			name: header.Name, mode: os.FileMode(header.Mode), data: data, linkTarget: header.Linkname,
		})
	}
	return entries
}

func cloneArchiveEntries(entries []archiveEntry) []archiveEntry {
	result := make([]archiveEntry, len(entries))
	for index, entry := range entries {
		result[index] = entry
		result[index].data = append([]byte(nil), entry.data...)
	}
	return result
}

func copyArtifactDirectory(t *testing.T, source string) string {
	t.Helper()
	target := t.TempDir()
	entries, err := os.ReadDir(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		writeTestFile(t, filepath.Join(target, entry.Name()), readTestFile(t, filepath.Join(source, entry.Name())))
	}
	return target
}

func gitTest(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", arguments[0], err, output)
	}
	return string(output)
}

func gitTestInput(
	t *testing.T,
	directory string,
	input string,
	arguments ...string,
) string {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	command.Stdin = strings.NewReader(input)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", arguments[0], err, output)
	}
	return string(output)
}

func readTestFile(t *testing.T, filename string) []byte {
	t.Helper()
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func writeTestFile(t *testing.T, filename string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filename, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeExistingTestFile(t *testing.T, filename string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filename, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeExistingOrNewTestFile(t *testing.T, filename string, data []byte) {
	t.Helper()
	writeExistingTestFile(t, filename, data)
}
