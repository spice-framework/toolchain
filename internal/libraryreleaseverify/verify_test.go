package libraryreleaseverify

import (
	"archive/tar"
	"bufio"
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
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	testRepositoryName = "starter-test"
	testModulePath     = "github.com/spice-framework/starter-test"
	testVersion        = "v1.2.3"
	testEpoch          = int64(1_700_000_000)
)

type releaseFixture struct {
	repository string
	directory  string
	commit     string
	publicPEM  []byte
	privateKey ed25519.PrivateKey
	source     []sourceEntry
}

func TestVerifyAuthenticatesTrustedLibraryRelease(t *testing.T) {
	t.Parallel()
	fixture := newReleaseFixture(t)
	result, err := Verify(context.Background(), fixture.config())
	if err != nil {
		t.Fatal(err)
	}
	if result.Commit != fixture.commit || result.Module != testModulePath ||
		!result.Epoch.Equal(time.Unix(testEpoch, 0).UTC()) || len(result.Files) != 5 {
		t.Fatalf("Verify() = %#v", result)
	}
	if !slices.IsSorted(result.Files) {
		t.Fatalf("Verify() files are not sorted: %v", result.Files)
	}
}

func TestVerifyRejectsUntrustedOrMalformedInputs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*testing.T, *releaseFixture, *Config)
		want   string
	}{
		{
			name:   "canceled context",
			mutate: func(*testing.T, *releaseFixture, *Config) {},
			want:   "context canceled",
		},
		{name: "unsafe repository", mutate: func(_ *testing.T, _ *releaseFixture, config *Config) {
			config.RepositoryName = "../starter"
		}, want: "unsafe"},
		{name: "noncanonical version", mutate: func(_ *testing.T, _ *releaseFixture, config *Config) {
			config.Version = "1.2.3"
		}, want: "not canonical"},
		{name: "uppercase commit", mutate: func(_ *testing.T, _ *releaseFixture, config *Config) {
			config.Commit = strings.ToUpper(config.Commit)
		}, want: "lowercase"},
		{name: "invalid key", mutate: func(_ *testing.T, _ *releaseFixture, config *Config) {
			config.TrustedPublicKey = []byte("not PEM")
		}, want: "PUBLIC KEY"},
		{name: "extra artifact", mutate: func(t *testing.T, fixture *releaseFixture, _ *Config) {
			t.Helper()
			writeTestFile(t, fixture.directory, "extra", []byte("x"))
		}, want: "artifact set"},
		{name: "wrong emitted key", mutate: func(t *testing.T, fixture *releaseFixture, _ *Config) {
			t.Helper()
			publicKey, _, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			writeTestFile(t, fixture.directory, "checksums.txt.pem", encodePublicKey(t, publicKey))
		}, want: "does not match"},
		{name: "invalid signature", mutate: func(t *testing.T, fixture *releaseFixture, _ *Config) {
			t.Helper()
			name := filepath.Join(fixture.directory, "checksums.txt.sig")
			signature, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			signature[0] ^= 0xff
			if err := os.WriteFile(name, signature, 0o600); err != nil {
				t.Fatal(err)
			}
		}, want: "signature is invalid"},
		{name: "noncanonical checksums", mutate: func(t *testing.T, fixture *releaseFixture, _ *Config) {
			t.Helper()
			checksums := []byte("invalid\n")
			writeTestFile(t, fixture.directory, "checksums.txt", checksums)
			writeTestFile(t, fixture.directory, "checksums.txt.sig", ed25519.Sign(fixture.privateKey, checksums))
		}, want: "require 2"},
		{name: "authenticated archive corruption", mutate: func(t *testing.T, fixture *releaseFixture, _ *Config) {
			t.Helper()
			archiveName := testRepositoryName + "_1.2.3_source.tar.gz"
			writeTestFile(t, fixture.directory, archiveName, []byte("not gzip"))
			resignArtifacts(t, fixture)
		}, want: "open gzip"},
		{name: "archive differs from commit", mutate: func(t *testing.T, fixture *releaseFixture, _ *Config) {
			t.Helper()
			changed := cloneSourceEntries(fixture.source)
			for index := range changed {
				if changed[index].path == "library.go" {
					changed[index].data = []byte("package testlib\n\nfunc Value() int { return 2 }\n")
				}
			}
			archive := buildTestArchive(t, changed)
			writeTestFile(t, fixture.directory, testRepositoryName+"_1.2.3_source.tar.gz", archive)
			resignArtifacts(t, fixture)
		}, want: "does not match trusted path"},
		{name: "SBOM differs from trusted graph", mutate: func(t *testing.T, fixture *releaseFixture, _ *Config) {
			t.Helper()
			name := filepath.Join(fixture.directory, testRepositoryName+"_1.2.3_sbom.spdx.json")
			data, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			data = bytes.Replace(data, []byte("Spice Framework"), []byte("Untrusted Org"), 1)
			if err := os.WriteFile(name, data, 0o600); err != nil {
				t.Fatal(err)
			}
			resignArtifacts(t, fixture)
		}, want: "does not exactly match"},
		{name: "wrong trusted origin", mutate: func(t *testing.T, fixture *releaseFixture, _ *Config) {
			t.Helper()
			runGit(t, fixture.repository, "remote", "set-url", "origin", "https://github.com/spice-framework/other.git")
		}, want: "trusted origin repository"},
		{name: "wrong host with same repository name", mutate: func(t *testing.T, fixture *releaseFixture, _ *Config) {
			t.Helper()
			runGit(t, fixture.repository, "remote", "set-url", "origin", "https://example.invalid/acme/starter-test.git")
		}, want: "require canonical source"},
		{name: "module mismatch", mutate: func(_ *testing.T, _ *releaseFixture, config *Config) {
			config.Module = "example.com/not-the-trusted-module"
		}, want: "trusted source module"},
		{name: "invalid trusted module", mutate: func(_ *testing.T, _ *releaseFixture, config *Config) {
			config.Module = "invalid module path"
		}, want: "trusted module"},
		{name: "noncanonical trusted source", mutate: func(_ *testing.T, _ *releaseFixture, config *Config) {
			config.CanonicalSource += ".git"
		}, want: "must use exact HTTPS form"},
		{name: "unknown commit", mutate: func(_ *testing.T, _ *releaseFixture, config *Config) {
			config.Commit = strings.Repeat("0", 40)
		}, want: "resolve trusted library commit"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newReleaseFixture(t)
			config := fixture.config()
			test.mutate(t, &fixture, &config)
			ctx := context.Background()
			if test.name == "canceled context" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				config.Repository = fixture.repository
			}
			_, err := Verify(ctx, config)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Verify() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestStrictParsersAndPortablePaths(t *testing.T) {
	t.Parallel()
	if !canonicalVersion("v2.0.0+incompatible") || !canonicalVersion("v1.2.3+build.7") ||
		canonicalVersion("v1.2") {
		t.Fatal("canonicalVersion does not match the renderer's full Go-version contract")
	}
	if rendererV1MaxCompatibilityMetadataBytes != 64<<10 ||
		rendererV1MaxModuleGraphBytes != 16<<20 || rendererV1MaxGoSumBytes != 16<<20 ||
		rendererV1MaxSBOMBytes != 1<<20 || rendererV1MaxSourceEntryBytes != 128<<20 ||
		rendererV1MaxSourceExpandedBytes != 256<<20 {
		t.Fatal("verifier control-file and source limits differ from renderer/v1")
	}
	if source, name, err := canonicalSourceURL("git@github.com:Spice-Framework/starter-test.git"); err != nil ||
		source != "https://github.com/Spice-Framework/starter-test" || name != testRepositoryName {
		t.Fatalf("canonicalSourceURL() = %q, %q, %v", source, name, err)
	}
	if source, name, err := trustedCanonicalSource(
		"https://github.com/spice-framework/starter-test",
	); err != nil || source != "https://github.com/spice-framework/starter-test" ||
		name != testRepositoryName {
		t.Fatalf("trustedCanonicalSource() = %q, %q, %v", source, name, err)
	}
	for _, value := range []string{
		"git@github.com:spice-framework/starter-test.git",
		"https://github.com/spice-framework/starter-test.git",
	} {
		if _, _, err := trustedCanonicalSource(value); err == nil {
			t.Errorf("trustedCanonicalSource(%q) succeeded", value)
		}
	}
	for _, value := range []string{
		"http://github.com/org/repo", "https://user@github.com/org/repo", "ssh://other@github.com/org/repo",
		"https://github.com/../repo", "not a URL",
	} {
		if _, _, err := canonicalSourceURL(value); err == nil {
			t.Errorf("canonicalSourceURL(%q) succeeded", value)
		}
	}
	for _, name := range []string{"../escape", `back\\slash`, "CON/file", "trailing./file", "control\x01"} {
		if err := validateArchivePath(name); err == nil {
			t.Errorf("validateArchivePath(%q) succeeded", name)
		}
	}
	if err := validateArchivePath("internal/pkg/file.go"); err != nil {
		t.Fatalf("validateArchivePath(valid) = %v", err)
	}
	if !safeLinkTarget("internal/latest", "../README.md") || safeLinkTarget("latest", "../escape") {
		t.Fatal("safeLinkTarget traversal policy is incorrect")
	}

	digest := sha256.Sum256([]byte("artifact"))
	validChecksums := []byte(hex.EncodeToString(digest[:]) + "  artifact\n")
	parsed, err := parseChecksums(validChecksums, []string{"artifact"})
	if err != nil || parsed["artifact"] != digest {
		t.Fatalf("parseChecksums(valid) = %#v, %v", parsed, err)
	}
	for _, invalid := range [][]byte{
		nil,
		bytes.TrimSuffix(validChecksums, []byte("\n")),
		bytes.ReplaceAll(validChecksums, []byte("\n"), []byte("\r\n")),
		[]byte(strings.Repeat("z", 64) + "  artifact\n"),
	} {
		if _, err := parseChecksums(invalid, []string{"artifact"}); err == nil {
			t.Errorf("parseChecksums(%q) succeeded", invalid)
		}
	}
}

func TestSourceModuleValidation(t *testing.T) {
	t.Parallel()
	valid := testSourceFiles()
	modules, modulePath, err := sourceModules(valid)
	if err != nil || modulePath != testModulePath || len(modules) != 2 || modules[0].path != "example.com/dependency" {
		t.Fatalf("sourceModules(valid) = %#v, %q, %v", modules, modulePath, err)
	}
	tests := []struct {
		name   string
		mutate func(map[string][]byte)
		want   string
	}{
		{name: "missing module", mutate: func(files map[string][]byte) { files["go.mod"] = []byte("go 1.26.0\n") }, want: "module directive"},
		{name: "local replacement", mutate: func(files map[string][]byte) {
			files["go.mod"] = append(files["go.mod"], []byte("replace example.com/dependency => ../dependency\n")...)
		}, want: "local or invalid replacement"},
		{name: "bad compatibility", mutate: func(files map[string][]byte) { files["spice-compatibility.json"] = []byte(`{"schema":2}`) }, want: "invalid"},
		{name: "core mismatch", mutate: func(files map[string][]byte) {
			files["spice-compatibility.json"] = []byte("{\"schema\":1,\"minimum\":\"v0.1.1\",\"current\":\"v0.2.0\"}\n")
		}, want: "compatibility minimum"},
		{name: "missing sum", mutate: func(files map[string][]byte) {
			files["go.sum"] = []byte("example.com/dependency v1.2.3 h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n")
		}, want: "no checksum"},
		{name: "missing vendor", mutate: func(files map[string][]byte) { files["vendor/modules.txt"] = nil }, want: "empty"},
		{name: "vendor not explicit", mutate: func(files map[string][]byte) {
			files["vendor/modules.txt"] = bytes.Replace(files["vendor/modules.txt"], []byte("## explicit"), []byte("## go 1.26.0"), 1)
		}, want: "explicit marker"},
		{name: "vendor drift", mutate: func(files map[string][]byte) {
			files["vendor/modules.txt"] = bytes.Replace(files["vendor/modules.txt"], []byte("v1.2.3"), []byte("v1.2.4"), 1)
		}, want: "is v1.2.4"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			files := cloneFileMap(valid)
			test.mutate(files)
			_, _, err := sourceModules(files)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("sourceModules() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestSourceModuleRemoteReplacement(t *testing.T) {
	t.Parallel()
	files := testSourceFiles()
	files["go.mod"] = append(
		files["go.mod"],
		[]byte("\nreplace example.com/dependency v1.2.3 => example.com/fork v1.2.4\n")...,
	)
	files["go.sum"] = append(
		files["go.sum"],
		[]byte("example.com/fork v1.2.4/go.mod h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n")...,
	)
	files["vendor/modules.txt"] = []byte(
		"# example.com/dependency v1.2.3 => example.com/fork v1.2.4\n" +
			"## explicit; go 1.26.0\nexample.com/dependency\n" +
			"# example.com/dependency => example.com/fork v1.2.4\n" +
			"# github.com/spice-framework/spice v0.1.0\n" +
			"## explicit; go 1.26.0\ngithub.com/spice-framework/spice\n",
	)
	modules, _, err := sourceModules(files)
	if err != nil {
		t.Fatal(err)
	}
	if len(modules) != 2 || modules[0].replacement != "example.com/fork v1.2.4" {
		t.Fatalf("sourceModules(replacement) = %#v", modules)
	}
	item := newSPDXPackage(modules[0].path, modules[0].version, modules[0].replacement)
	if len(item.ExternalRefs) != 1 || item.ExternalRefs[0].ReferenceLocator != modules[0].replacement {
		t.Fatalf("newSPDXPackage(replacement) = %#v", item)
	}
	if path, replacement, ok := parseVendorReplacementMarker(
		"example.com/dependency => example.com/fork v1.2.4",
	); !ok || path != "example.com/dependency" || replacement != "example.com/fork v1.2.4" {
		t.Fatalf("parseVendorReplacementMarker() = %q, %q, %t", path, replacement, ok)
	}
}

func TestCompatibilityAndVendorGraphValidation(t *testing.T) {
	t.Parallel()
	valid := []byte("{\"schema\":1,\"minimum\":\"v0.1.0\",\"current\":\"v0.2.0\"}\n")
	metadata, err := parseCompatibility(valid)
	if err != nil || metadata.Minimum != "v0.1.0" {
		t.Fatalf("parseCompatibility(valid) = %#v, %v", metadata, err)
	}
	for _, data := range [][]byte{
		nil,
		[]byte("{"),
		[]byte("{\"schema\":1,\"minimum\":\"v0.1.0\",\"current\":\"v0.2.0\"} {}"),
		[]byte("{\"schema\":2,\"schema\":1,\"minimum\":\"v0.1.0\",\"current\":\"v0.2.0\"}"),
		[]byte("{\"schema\":1,\"minimum\":\"v0.1.0\",\"current\":\"v0.2.0\",\"extra\":true}"),
		[]byte("{\"schema\":1,\"minimum\":\"v0.1.0\",\"current\":\"v0.1.0\"}"),
	} {
		if _, err := parseCompatibility(data); err == nil {
			t.Errorf("parseCompatibility(%q) succeeded", data)
		}
	}
	want := []listedModule{{path: "a", version: "v1.0.0"}}
	for _, test := range []struct {
		name   string
		actual []listedModule
	}{
		{name: "missing"},
		{name: "extra", actual: []listedModule{{path: "b", version: "v1.0.0"}}},
		{name: "drift", actual: []listedModule{{path: "a", version: "v1.0.1"}}},
		{name: "duplicate", actual: []listedModule{{path: "a", version: "v1.0.0"}, {path: "a", version: "v1.0.0"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := validateVendorGraph(want, test.actual); err == nil {
				t.Fatal("validateVendorGraph() error = nil")
			}
		})
	}
	for _, header := range []string{
		"bad", "example.com/a latest", "example.com/a v1.0.0 => ../local",
	} {
		if _, ok := parseVendorModule(header); ok {
			t.Errorf("parseVendorModule(%q) succeeded", header)
		}
	}
}

func TestAuthenticatedArtifactAndSignatureBoundaries(t *testing.T) {
	t.Parallel()
	fixture := newReleaseFixture(t)
	checksums, readErr := readBoundedRegularFile(fixture.directory, "checksums.txt", maxChecksumsBytes)
	if readErr != nil {
		t.Fatal(readErr)
	}
	publicKey, keyErr := parsePublicKey(fixture.publicPEM, "trusted")
	if keyErr != nil {
		t.Fatal(keyErr)
	}
	if authenticationErr := authenticateChecksums(fixture.directory, checksums, publicKey); authenticationErr != nil {
		t.Fatal(authenticationErr)
	}
	writeTestFile(t, fixture.directory, "checksums.txt.sig", []byte("short"))
	if authenticationErr := authenticateChecksums(fixture.directory, checksums, publicKey); authenticationErr == nil ||
		!strings.Contains(authenticationErr.Error(), "signature length") {
		t.Fatalf("authenticateChecksums(short) = %v", authenticationErr)
	}

	name := testRepositoryName + "_1.2.3_source.tar.gz"
	digest := sha256.Sum256([]byte("different"))
	if _, artifactErr := readAuthenticatedArtifact(context.Background(), fixture.directory, name, digest); artifactErr == nil ||
		!strings.Contains(artifactErr.Error(), "SHA-256 mismatch") {
		t.Fatalf("readAuthenticatedArtifact(hash mismatch) = %v", artifactErr)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	actual, readErr := os.ReadFile(filepath.Join(fixture.directory, name))
	if readErr != nil {
		t.Fatal(readErr)
	}
	actualDigest := sha256.Sum256(actual)
	if _, artifactErr := readAuthenticatedArtifact(ctx, fixture.directory, name, actualDigest); artifactErr == nil ||
		!strings.Contains(artifactErr.Error(), "context canceled") {
		t.Fatalf("readAuthenticatedArtifact(canceled) = %v", artifactErr)
	}
}

func TestGitTreeValidation(t *testing.T) {
	t.Parallel()
	objectID := strings.Repeat("a", 40)
	valid := []byte("100644 blob " + objectID + "\tfile.go\x00")
	entries, err := parseGitTree(valid)
	if err != nil || len(entries) != 1 || entries[0].path != "file.go" || entries[0].mode != 0o644 {
		t.Fatalf("parseGitTree(valid) = %#v, %v", entries, err)
	}
	for _, test := range []struct {
		name string
		data []byte
	}{
		{name: "empty"},
		{name: "no tab", data: []byte("100644 blob " + objectID)},
		{name: "tree", data: []byte("040000 tree " + objectID + "\tdir\x00")},
		{name: "bad mode", data: []byte("100600 blob " + objectID + "\tfile\x00")},
		{name: "bad object", data: []byte("100644 blob invalid\tfile\x00")},
		{name: "unsafe path", data: []byte("100644 blob " + objectID + "\t../file\x00")},
		{name: "collision", data: []byte(
			"100644 blob " + objectID + "\tFile\x00" +
				"100755 blob " + objectID + "\tfile\x00",
		)},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := parseGitTree(test.data); err == nil {
				t.Fatal("parseGitTree() error = nil")
			}
		})
	}
	if err := validateExactCommit(strings.Repeat("b", 64)); err != nil {
		t.Fatalf("validateExactCommit(SHA-256) = %v", err)
	}
	for _, commit := range []string{"short", strings.Repeat("z", 40)} {
		if err := validateExactCommit(commit); err == nil {
			t.Errorf("validateExactCommit(%q) succeeded", commit)
		}
	}
}

func TestSourceArchiveRejectsNoncanonicalContent(t *testing.T) {
	t.Parallel()
	epoch := time.Unix(testEpoch, 0).UTC()
	canonical := func() *tar.Header {
		return &tar.Header{
			Name: "starter-test_1.2.3/file", Mode: 0o644, Size: 1,
			Typeflag: tar.TypeReg, ModTime: epoch, AccessTime: epoch,
			ChangeTime: epoch, Format: tar.FormatPAX,
		}
	}
	tests := []struct {
		name      string
		modify    func(*tar.Header)
		duplicate bool
		gzipEpoch time.Time
		trailing  bool
		cancel    bool
	}{
		{name: "gzip epoch", gzipEpoch: epoch.Add(time.Second)},
		{name: "unsafe path", modify: func(header *tar.Header) { header.Name = "../escape" }},
		{name: "metadata", modify: func(header *tar.Header) { header.Uid = 1 }},
		{name: "PAX metadata", modify: func(header *tar.Header) {
			header.PAXRecords = map[string]string{"comment": "not renderer/v1"}
		}},
		{name: "extended attribute", modify: func(header *tar.Header) {
			//nolint:staticcheck // Exercise rejection of archive/tar's legacy Xattrs compatibility view.
			header.Xattrs = map[string]string{"user.spice": "not renderer/v1"}
		}},
		{name: "device metadata", modify: func(header *tar.Header) { header.Devmajor = 1 }},
		{name: "regular link target", modify: func(header *tar.Header) { header.Linkname = "other" }},
		{name: "mode", modify: func(header *tar.Header) { header.Mode = 0o1000 }},
		{name: "unsupported type", modify: func(header *tar.Header) { header.Typeflag = tar.TypeDir; header.Size = 0 }},
		{name: "unsafe symlink", modify: func(header *tar.Header) {
			header.Typeflag = tar.TypeSymlink
			header.Size = 0
			header.Linkname = "../../escape"
		}},
		{name: "duplicate", duplicate: true},
		{name: "trailing", trailing: true},
		{name: "canceled", cancel: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			header := canonical()
			if test.modify != nil {
				test.modify(header)
			}
			gzipEpoch := test.gzipEpoch
			if gzipEpoch.IsZero() {
				gzipEpoch = epoch
			}
			headers := []*tar.Header{header}
			if test.duplicate {
				duplicate := *header
				headers = append(headers, &duplicate)
			}
			archive := buildRawTestArchive(t, gzipEpoch, headers, test.trailing)
			ctx := context.Background()
			if test.cancel {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if _, err := readSourceArchive(ctx, archive, epoch); err == nil {
				t.Fatal("readSourceArchive() error = nil")
			}
		})
	}
	validHeader := canonical()
	valid := buildRawTestArchive(t, epoch, []*tar.Header{validHeader}, false)
	corruptCRC := slices.Clone(valid)
	corruptCRC[len(corruptCRC)-8] ^= 0xff
	truncated := slices.Clone(valid[:len(valid)-4])
	hidden := buildRawTestArchiveWithTail(t, epoch, []*tar.Header{canonical()}, []byte("hidden"), false)
	for name, archive := range map[string][]byte{
		"corrupt CRC":              corruptCRC,
		"truncated trailer":        truncated,
		"hidden decompressed tail": hidden,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := readSourceArchive(context.Background(), archive, epoch); err == nil {
				t.Fatal("readSourceArchive() error = nil")
			}
		})
	}
}

func TestRendererV1TarPAXBoundaries(t *testing.T) {
	t.Parallel()
	epoch := time.Unix(testEpoch, 0).UTC()
	const base = "starter-test_1.2.3"
	for _, test := range []struct {
		name       string
		path       string
		linkTarget string
	}{
		{name: "100-byte path", path: strings.Repeat("a", 81)},
		{name: "101-byte path", path: strings.Repeat("a", 82)},
		{name: "Unicode path", path: "café.go"},
		{name: "100-byte link", path: "link", linkTarget: strings.Repeat("a", 100)},
		{name: "101-byte link", path: "link", linkTarget: strings.Repeat("a", 101)},
		{name: "Unicode link", path: "link", linkTarget: "café.go"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			header := &tar.Header{
				Name: path.Join(base, test.path), Mode: 0o644, Size: 1,
				Typeflag: tar.TypeReg, ModTime: epoch, AccessTime: epoch,
				ChangeTime: epoch, Format: tar.FormatPAX,
			}
			expected := sourceEntry{path: test.path, mode: 0o644, data: []byte("x")}
			if test.linkTarget != "" {
				header.Mode = 0o777
				header.Size = 0
				header.Typeflag = tar.TypeSymlink
				header.Linkname = test.linkTarget
				expected.mode = 0o777
				expected.data = nil
				expected.linkTarget = test.linkTarget
			}
			archive := buildRawTestArchive(t, epoch, []*tar.Header{header}, false)
			actual, err := readSourceArchive(context.Background(), archive, epoch)
			if err != nil {
				t.Fatalf("readSourceArchive() = %v", err)
			}
			if len(actual) != 1 || actual[0].name != path.Join(base, expected.path) ||
				actual[0].mode != expected.mode || actual[0].linkTarget != expected.linkTarget ||
				!bytes.Equal(actual[0].data, expected.data) {
				t.Fatalf("readSourceArchive() entry = %#v", actual)
			}
		})
	}
}

func newReleaseFixture(t *testing.T) releaseFixture {
	t.Helper()
	repository := filepath.Join(t.TempDir(), "repository")
	if err := os.Mkdir(repository, 0o750); err != nil {
		t.Fatal(err)
	}
	runGit(t, repository, "init", "--quiet")
	runGit(t, repository, "config", "user.name", "Spice Tests")
	runGit(t, repository, "config", "user.email", "tests@spice.invalid")
	runGit(t, repository, "remote", "add", "origin", "https://github.com/spice-framework/"+testRepositoryName+".git")
	files := testSourceFiles()
	files["LICENSE"] = []byte("Apache License 2.0 fixture\n")
	files["README.md"] = []byte("# Test starter\n")
	files["library.go"] = []byte("package testlib\n\nfunc Value() int { return 1 }\n")
	for name, content := range files {
		writeTestFile(t, repository, name, content)
	}
	runGit(t, repository, "add", ".")
	command := exec.Command("git", "commit", "--quiet", "-m", "fixture")
	command.Dir = repository
	command.Env = append(
		os.Environ(),
		"GIT_AUTHOR_DATE=@1700000000 +0000",
		"GIT_COMMITTER_DATE=@1700000000 +0000",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v: %s", err, output)
	}
	commit := strings.TrimSpace(runGit(t, repository, "rev-parse", "HEAD"))
	identity, source, err := trustedSource(context.Background(), repository, commit)
	if err != nil {
		t.Fatal(err)
	}
	archive := buildTestArchive(t, source)
	trustedFiles := make(map[string][]byte, len(source))
	for _, entry := range source {
		if entry.linkTarget == "" {
			trustedFiles[entry.path] = slices.Clone(entry.data)
		}
	}
	modules, modulePath, err := sourceModules(trustedFiles)
	if err != nil {
		t.Fatal(err)
	}
	sbom := expectedSBOM(sbomIdentity{
		repository: testRepositoryName, module: modulePath, source: identity.source,
		version: testVersion, commit: commit, epoch: identity.epoch,
	}, modules)
	sbomBytes, err := json.MarshalIndent(sbom, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	sbomBytes = append(sbomBytes, '\n')
	directory := filepath.Join(t.TempDir(), "artifacts")
	if directoryErr := os.Mkdir(directory, 0o750); directoryErr != nil {
		t.Fatal(directoryErr)
	}
	writeTestFile(t, directory, testRepositoryName+"_1.2.3_source.tar.gz", archive)
	writeTestFile(t, directory, testRepositoryName+"_1.2.3_sbom.spdx.json", sbomBytes)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	fixture := releaseFixture{
		repository: repository, directory: directory, commit: commit,
		publicPEM: encodePublicKey(t, publicKey), privateKey: privateKey, source: source,
	}
	writeTestFile(t, directory, "checksums.txt.pem", fixture.publicPEM)
	resignArtifacts(t, &fixture)
	return fixture
}

func (fixture releaseFixture) config() Config {
	return Config{
		Directory: fixture.directory, Repository: fixture.repository,
		RepositoryName:  testRepositoryName,
		CanonicalSource: "https://github.com/spice-framework/" + testRepositoryName,
		Module:          testModulePath, Version: testVersion, Commit: fixture.commit,
		TrustedPublicKey: fixture.publicPEM,
	}
}

func testSourceFiles() map[string][]byte {
	return map[string][]byte{
		"go.mod": []byte(
			"module " + testModulePath + "\n\ngo 1.26.0\n\nrequire (\n" +
				"\texample.com/dependency v1.2.3\n" +
				"\tgithub.com/spice-framework/spice v0.1.0\n)\n",
		),
		"go.sum": []byte(
			"example.com/dependency v1.2.3/go.mod h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n" +
				"github.com/spice-framework/spice v0.1.0/go.mod h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n",
		),
		"spice-compatibility.json": []byte("{\"schema\":1,\"minimum\":\"v0.1.0\",\"current\":\"v0.2.0\"}\n"),
		"vendor/modules.txt": []byte(
			"# example.com/dependency v1.2.3\n## explicit; go 1.26.0\nexample.com/dependency\n" +
				"# github.com/spice-framework/spice v0.1.0\n## explicit; go 1.26.0\ngithub.com/spice-framework/spice\n",
		),
	}
}

func buildTestArchive(t *testing.T, entries []sourceEntry) []byte {
	t.Helper()
	var output bytes.Buffer
	gzipWriter, err := gzip.NewWriterLevel(&output, gzip.BestCompression)
	if err != nil {
		t.Fatal(err)
	}
	epoch := time.Unix(testEpoch, 0).UTC()
	gzipWriter.ModTime = epoch
	gzipWriter.OS = 255
	tarWriter := tar.NewWriter(gzipWriter)
	for _, entry := range entries {
		header := &tar.Header{
			Name: testRepositoryName + "_1.2.3/" + entry.path,
			Mode: int64(entry.mode.Perm()), ModTime: epoch, AccessTime: epoch,
			ChangeTime: epoch, Format: tar.FormatPAX,
		}
		content := entry.data
		if entry.linkTarget != "" {
			header.Typeflag = tar.TypeSymlink
			header.Linkname = entry.linkTarget
			content = nil
		} else {
			header.Typeflag = tar.TypeReg
			header.Size = int64(len(content))
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write(content); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func buildRawTestArchive(
	t *testing.T,
	gzipEpoch time.Time,
	headers []*tar.Header,
	trailing bool,
) []byte {
	t.Helper()
	return buildRawTestArchiveWithTail(t, gzipEpoch, headers, nil, trailing)
}

func buildRawTestArchiveWithTail(
	t *testing.T,
	gzipEpoch time.Time,
	headers []*tar.Header,
	hidden []byte,
	trailing bool,
) []byte {
	t.Helper()
	var output bytes.Buffer
	gzipWriter, err := gzip.NewWriterLevel(&output, gzip.BestCompression)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter.ModTime = gzipEpoch
	gzipWriter.OS = 255
	tarWriter := tar.NewWriter(gzipWriter)
	for _, header := range headers {
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Typeflag == tar.TypeReg && header.Size > 0 {
			if _, err := tarWriter.Write(bytes.Repeat([]byte{'x'}, int(header.Size))); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := gzipWriter.Write(hidden); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if trailing {
		output.WriteByte('x')
	}
	return output.Bytes()
}

func resignArtifacts(t *testing.T, fixture *releaseFixture) {
	t.Helper()
	names := []string{
		testRepositoryName + "_1.2.3_sbom.spdx.json",
		testRepositoryName + "_1.2.3_source.tar.gz",
	}
	var checksums strings.Builder
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(fixture.directory, name))
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(data)
		fmt.Fprintf(&checksums, "%s  %s\n", hex.EncodeToString(digest[:]), name)
	}
	content := []byte(checksums.String())
	writeTestFile(t, fixture.directory, "checksums.txt", content)
	writeTestFile(t, fixture.directory, "checksums.txt.sig", ed25519.Sign(fixture.privateKey, content))
}

func encodePublicKey(t *testing.T, key ed25519.PublicKey) []byte {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
}

func writeTestFile(t *testing.T, root, name string, content []byte) {
	t.Helper()
	filename := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(filename), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = directory
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(arguments, " "), err, output)
	}
	return string(output)
}

func cloneSourceEntries(entries []sourceEntry) []sourceEntry {
	result := make([]sourceEntry, len(entries))
	for index, entry := range entries {
		result[index] = entry
		result[index].data = slices.Clone(entry.data)
	}
	return result
}

func cloneFileMap(source map[string][]byte) map[string][]byte {
	result := make(map[string][]byte, len(source))
	for name, content := range source {
		result[name] = slices.Clone(content)
	}
	return result
}

func TestVerifyRejectsNilContext(t *testing.T) {
	t.Parallel()
	//nolint:staticcheck // Deliberately exercise the exported nil-context boundary.
	_, err := Verify(nil, Config{})
	if err == nil || !strings.Contains(err.Error(), "context is nil") {
		t.Fatalf("Verify(nil) error = %v", err)
	}
}

func TestCentralRendererV1Acceptance(t *testing.T) {
	const (
		vectorEnvironment = "SPICE_LIBRARY_RELEASE_ACCEPTANCE_ROOT"
		vectorCommit      = "24ae4132e4782b8c0957c5d44b85cfcd845a168e"
		vectorModule      = "github.com/spice-framework/starter-oidc"
		vectorRepository  = "starter-oidc"
		vectorVersion     = "v1.2.3"
	)
	root := os.Getenv(vectorEnvironment)
	if root == "" {
		t.Skip("set " + vectorEnvironment + " to the pinned renderer/v1 acceptance-vector directory")
	}
	artifacts := filepath.Join(root, "central")
	trustedKey, err := os.ReadFile(filepath.Join(artifacts, "checksums.txt.pem"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := Verify(context.Background(), Config{
		Directory:        artifacts,
		Repository:       filepath.Join(root, vectorRepository),
		RepositoryName:   vectorRepository,
		CanonicalSource:  "https://github.com/spice-framework/" + vectorRepository,
		Module:           vectorModule,
		Version:          vectorVersion,
		Commit:           vectorCommit,
		TrustedPublicKey: trustedKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Commit != vectorCommit || result.Module != vectorModule || len(result.Files) != 5 {
		t.Fatalf("Verify(renderer/v1 vector) = %#v", result)
	}
}

func TestReadGitBlobRejectsMalformedStreams(t *testing.T) {
	t.Parallel()
	objectID := strings.Repeat("a", 40)
	for _, test := range []struct {
		name string
		data string
	}{
		{name: "missing header", data: ""},
		{name: "wrong object", data: strings.Repeat("b", 40) + " blob 1\nx\n"},
		{name: "oversize", data: objectID + " blob 999999999999\n"},
		{name: "short body", data: objectID + " blob 2\nx"},
		{name: "missing terminator", data: objectID + " blob 1\nx!"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := readGitBlob(bufio.NewReader(strings.NewReader(test.data)), objectID); err == nil {
				t.Fatal("readGitBlob() error = nil")
			}
		})
	}
}

func TestLimitedBufferReportsTruncation(t *testing.T) {
	t.Parallel()
	buffer := limitedBuffer{maximum: 3}
	written, err := buffer.Write([]byte("abcdef"))
	if err != nil || written != 6 || !buffer.truncated || buffer.String() != "abc" {
		t.Fatalf("limitedBuffer = %q, %d, %v, %v", buffer.String(), written, buffer.truncated, err)
	}
	if _, err := gitOutput(context.Background(), t.TempDir(), 1); err == nil {
		t.Fatal("gitOutput(no arguments) error = nil")
	}
}

func TestParsePublicKeyRejectsNoncanonicalData(t *testing.T) {
	t.Parallel()
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	canonical := encodePublicKey(t, publicKey)
	parsed, err := parsePublicKey(canonical, "test")
	if err != nil || !bytes.Equal(parsed, publicKey) {
		t.Fatalf("parsePublicKey(valid) = %x, %v", parsed, err)
	}
	for _, data := range [][]byte{
		nil,
		[]byte("not pem"),
		bytes.ReplaceAll(canonical, []byte("\n"), []byte("\r\n")),
		append(slices.Clone(canonical), '\n'),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("x")}),
	} {
		if _, err := parsePublicKey(data, "test"); err == nil {
			t.Errorf("parsePublicKey(%q) succeeded", data)
		}
	}
}

func TestVerifySBOMRejectsMalformedJSON(t *testing.T) {
	t.Parallel()
	identity := sbomIdentity{
		repository: testRepositoryName, module: testModulePath,
		source:  "https://github.com/spice-framework/" + testRepositoryName,
		version: testVersion, commit: strings.Repeat("a", 40), epoch: time.Unix(testEpoch, 0).UTC(),
	}
	modules := []listedModule{{path: coreModulePath, version: "v0.1.0"}}
	document := expectedSBOM(identity, modules)
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifySBOM(data, identity, modules); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range [][]byte{
		[]byte("{"),
		append(slices.Clone(data), []byte(" {}")...),
		bytes.Replace(
			data,
			[]byte(`"creationInfo":{`),
			[]byte(`"creationInfo":{"created":"2000-01-01T00:00:00Z",`),
			1,
		),
		bytes.Replace(data, []byte("SPDX-2.3"), []byte("SPDX-2.2"), 1),
	} {
		if err := verifySBOM(invalid, identity, modules); err == nil {
			t.Errorf("verifySBOM(%q) succeeded", invalid)
		}
	}
}

func TestRejectDuplicateJSONKeysRecursively(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		data string
	}{
		{name: "top level", data: `{"value":1,"value":2}`},
		{name: "nested object", data: `{"outer":{"value":1,"value":2}}`},
		{name: "object in array", data: `[{"value":1,"value":2}]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := rejectDuplicateJSONKeys([]byte(test.data)); err == nil ||
				!strings.Contains(err.Error(), "repeats key") {
				t.Fatalf("rejectDuplicateJSONKeys() error = %v", err)
			}
		})
	}
	if err := rejectDuplicateJSONKeys([]byte(`{"left":{"value":1},"right":{"value":2}}`)); err != nil {
		t.Fatalf("rejectDuplicateJSONKeys(unique) = %v", err)
	}
}

func TestReadBoundedRegularFileRejectsMissingAndOversized(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	if _, err := readBoundedRegularFile(directory, "missing", 1); err == nil {
		t.Fatal("readBoundedRegularFile(missing) error = nil")
	}
	if err := os.WriteFile(filepath.Join(directory, "large"), []byte("xx"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readBoundedRegularFile(directory, "large", 1); err == nil {
		t.Fatal("readBoundedRegularFile(oversized) error = nil")
	}
	if err := os.Mkdir(filepath.Join(directory, "dir"), 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := readBoundedRegularFile(directory, "dir", 1); err == nil {
		t.Fatal("readBoundedRegularFile(directory) error = nil")
	}
	if err := os.Symlink("large", filepath.Join(directory, "link")); err != nil {
		t.Logf("symlink boundary unavailable on this host: %v", err)
	} else if _, err := readBoundedRegularFile(directory, "link", 2); err == nil {
		t.Fatal("readBoundedRegularFile(symlink) error = nil")
	}
}

func TestCanonicalRemoteReplacement(t *testing.T) {
	t.Parallel()
	if got := canonicalRemoteReplacement("example.com/fork v1.2.3"); got != "example.com/fork v1.2.3" {
		t.Fatalf("canonicalRemoteReplacement(valid) = %q", got)
	}
	for _, value := range []string{"../fork v1.2.3", "example.com/fork latest", "example.com/fork"} {
		if got := canonicalRemoteReplacement(value); got != "" {
			t.Errorf("canonicalRemoteReplacement(%q) = %q", value, got)
		}
	}
}

func TestErrorsRemainInspectable(t *testing.T) {
	t.Parallel()
	cause := errors.New("cause")
	if !errors.Is(errors.Join(cause, nil), cause) {
		t.Fatal("joined release errors do not preserve causes")
	}
}
