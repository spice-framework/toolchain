package releaseverify

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSourceModulesStrictValidation(t *testing.T) {
	t.Parallel()
	validMod := []byte("module " + modulePath + "\n\nrequire example.com/dependency v1.2.3\n")
	validVendor := []byte("# example.com/dependency v1.2.3\n## explicit; go 1.26.0\nexample.com/dependency/pkg\n")
	modules, err := sourceModules(map[string][]byte{
		"go.mod": validMod, "vendor/modules.txt": validVendor,
	})
	if err != nil || len(modules) != 2 || modules[0].main ||
		modules[0].path != "example.com/dependency" || modules[0].version != "v1.2.3" ||
		!modules[1].main || modules[1].path != modulePath {
		t.Fatalf("sourceModules(valid) = %#v, %v", modules, err)
	}
	tests := []struct {
		name   string
		goMod  []byte
		vendor []byte
		want   string
	}{
		{name: "malformed go.mod", goMod: []byte("module"), want: "parse source go.mod"},
		{name: "wrong module", goMod: []byte("module example.com/wrong\n"), want: "source module"},
		{
			name: "replacement", goMod: []byte(
				"module " + modulePath + "\nreplace example.com/a => ./a\n",
			), want: "replace directives",
		},
		{
			name: "invalid vendor header", goMod: validMod,
			vendor: []byte("# example.com/dependency invalid\n## explicit\n"), want: "header",
		},
		{
			name: "not explicit", goMod: validMod,
			vendor: []byte("# example.com/dependency v1.2.3\n## go 1.26.0\n"), want: "header",
		},
		{
			name: "duplicate vendor", goMod: validMod,
			vendor: []byte(
				"# example.com/dependency v1.2.3\n## explicit\n" +
					"# example.com/dependency v1.2.3\n## explicit\n",
			), want: "duplicated",
		},
		{name: "missing required", goMod: validMod, vendor: nil, want: "does not contain exact"},
		{
			name: "wrong version", goMod: validMod,
			vendor: []byte("# example.com/dependency v1.2.4\n## explicit\n"),
			want:   "does not contain exact",
		},
		{
			name: "undeclared vendor", goMod: []byte("module " + modulePath + "\n"),
			vendor: []byte("# example.com/dependency v1.2.3\n## explicit\n"),
			want:   "undeclared",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := sourceModules(map[string][]byte{
				"go.mod": test.goMod, "vendor/modules.txt": test.vendor,
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("sourceModules() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestParseVendoredModule(t *testing.T) {
	t.Parallel()
	if module, ok := parseVendoredModule("example.com/a v1.2.3"); !ok ||
		module.path != "example.com/a" || module.version != "v1.2.3" {
		t.Fatalf("parseVendoredModule(valid) = %#v, %t", module, ok)
	}
	for _, line := range []string{
		"example.com/a v1.2.3 => ../a", "example.com/a", "example.com/a latest",
	} {
		if _, ok := parseVendoredModule(line); ok {
			t.Errorf("parseVendoredModule(%q) succeeded", line)
		}
	}
}

func TestPublicKeyParsingIsCanonicalAndEd25519(t *testing.T) {
	t.Parallel()
	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	canonical := encodeTestPublicKey(t, publicKey)
	if parsed, parseErr := parsePEMPublicKey(canonical, "test key"); parseErr != nil ||
		!bytes.Equal(parsed, publicKey) {
		t.Fatalf("parsePEMPublicKey(valid) = %x, %v", parsed, parseErr)
	}
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{name: "empty", want: "invalid size"},
		{name: "oversize", data: bytes.Repeat([]byte{'x'}, maxPublicKeyBytes+1), want: "invalid size"},
		{name: "not PEM", data: []byte("not pem"), want: "one PUBLIC KEY"},
		{
			name: "wrong block", data: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("x")}),
			want: "one PUBLIC KEY",
		},
		{
			name: "headers", data: pem.EncodeToMemory(&pem.Block{
				Type: "PUBLIC KEY", Headers: map[string]string{"X": "Y"}, Bytes: []byte("x"),
			}), want: "one PUBLIC KEY",
		},
		{name: "invalid DER", data: pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: []byte("x")}), want: "parse test key"},
		{name: "trailing block", data: append(append([]byte(nil), canonical...), canonical...), want: "one PUBLIC KEY"},
		{name: "noncanonical line endings", data: bytes.ReplaceAll(canonical, []byte("\n"), []byte("\r\n")), want: "not canonical"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, parseErr := parsePEMPublicKey(test.data, "test key")
			if parseErr == nil || !strings.Contains(parseErr.Error(), test.want) {
				t.Fatalf("parsePEMPublicKey() error = %v, want containing %q", parseErr, test.want)
			}
		})
	}
	ecdsaKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	encodedECDSA, err := x509.MarshalPKIXPublicKey(&ecdsaKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	_, err = parsePEMPublicKey(
		pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: encodedECDSA}),
		"test key",
	)
	if err == nil || !strings.Contains(err.Error(), "require Ed25519") {
		t.Fatalf("parsePEMPublicKey(ECDSA) error = %v", err)
	}
}

func TestParseChecksumsStrictGrammar(t *testing.T) {
	t.Parallel()
	digest := sha256.Sum256([]byte("artifact"))
	valid := []byte(hex.EncodeToString(digest[:]) + "  artifact.tar.gz\n")
	parsed, err := parseChecksums(valid, []string{"artifact.tar.gz"})
	if err != nil || parsed["artifact.tar.gz"] != digest {
		t.Fatalf("parseChecksums(valid) = %#v, %v", parsed, err)
	}
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{name: "empty", want: "LF-terminated"},
		{name: "no newline", data: bytes.TrimSuffix(valid, []byte("\n")), want: "LF-terminated"},
		{name: "CRLF", data: bytes.ReplaceAll(valid, []byte("\n"), []byte("\r\n")), want: "LF-terminated"},
		{name: "wrong count", data: append(append([]byte(nil), valid...), valid...), want: "2 lines"},
		{name: "wrong name", data: []byte(hex.EncodeToString(digest[:]) + "  other.tar.gz\n"), want: "not canonical"},
		{name: "uppercase digest", data: []byte(strings.ToUpper(hex.EncodeToString(digest[:])) + "  artifact.tar.gz\n"), want: "not canonical"},
		{name: "invalid digest", data: []byte(strings.Repeat("z", 64) + "  artifact.tar.gz\n"), want: "invalid SHA-256"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseChecksums(test.data, []string{"artifact.tar.gz"})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("parseChecksums() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestParseGitTreeAndObjectIDs(t *testing.T) {
	t.Parallel()
	objectID := strings.Repeat("a", 40)
	valid := []byte("100644 blob " + objectID + "\tfile.go\x00")
	entries, err := parseGitTree(valid)
	if err != nil || len(entries) != 1 || entries[0].path != "file.go" || entries[0].mode != 0o644 {
		t.Fatalf("parseGitTree(valid) = %#v, %v", entries, err)
	}
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{name: "empty", data: nil, want: "empty"},
		{name: "empty record", data: []byte("\x00\x00"), want: "entry 0 is empty"},
		{name: "no tab", data: []byte("100644 blob " + objectID + "\x00"), want: "unsupported metadata"},
		{name: "tree object", data: []byte("100644 tree " + objectID + "\tfile.go\x00"), want: "unsupported metadata"},
		{name: "unsafe path", data: []byte("100644 blob " + objectID + "\t../file.go\x00"), want: "unsafe"},
		{name: "bad mode", data: []byte("100600 blob " + objectID + "\tfile.go\x00"), want: "unsupported mode"},
		{name: "bad object", data: []byte("100644 blob invalid\tfile.go\x00"), want: "object ID"},
		{
			name: "case collision", data: []byte(
				"100644 blob " + objectID + "\tFile.go\x00" +
					"100644 blob " + objectID + "\tfile.go\x00",
			), want: "collide",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseGitTree(test.data)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("parseGitTree() error = %v, want containing %q", err, test.want)
			}
		})
	}
	if err := validateGitObjectID(objectID); err != nil {
		t.Fatalf("validateGitObjectID(valid) error = %v", err)
	}
	for _, invalid := range []string{"short", strings.Repeat("z", 40)} {
		if err := validateGitObjectID(invalid); err == nil {
			t.Errorf("validateGitObjectID(%q) succeeded", invalid)
		}
	}
}

func TestParseGitTreeAcceptsExecutableAndSymlinkModes(t *testing.T) {
	t.Parallel()
	objectID := strings.Repeat("a", 40)
	entries, err := parseGitTree([]byte(
		"100755 blob " + objectID + "\tcmd/spice/main.go\x00" +
			"120000 blob " + objectID + "\tlatest\x00",
	))
	if err != nil || len(entries) != 2 || entries[0].mode != 0o755 ||
		entries[1].mode != 0o777 {
		t.Fatalf("parseGitTree(supported modes) = %#v, %v", entries, err)
	}
	if err := validateExactCommit(strings.Repeat("b", 64)); err != nil {
		t.Fatalf("validateExactCommit(SHA-256) error = %v", err)
	}
	if err := validateExactCommit(strings.Repeat("z", 40)); err == nil ||
		!strings.Contains(err.Error(), "not hexadecimal") {
		t.Fatalf("validateExactCommit(invalid hex) error = %v", err)
	}
}

func TestReadGitBlobValidation(t *testing.T) {
	t.Parallel()
	objectID := strings.Repeat("a", 40)
	valid := objectID + " blob 4\ndata\n"
	data, err := readGitBlob(bufio.NewReader(strings.NewReader(valid)), objectID)
	if err != nil || string(data) != "data" {
		t.Fatalf("readGitBlob(valid) = %q, %v", data, err)
	}
	tests := []struct {
		name string
		data string
		want string
	}{
		{name: "missing header", want: "header"},
		{name: "wrong object", data: strings.Repeat("b", 40) + " blob 0\n\n", want: "invalid metadata"},
		{name: "wrong type", data: objectID + " tree 0\n\n", want: "invalid metadata"},
		{name: "bad size", data: objectID + " blob invalid\n", want: "entry-size limit"},
		{name: "short data", data: objectID + " blob 4\nx", want: "read git blob"},
		{name: "bad terminator", data: objectID + " blob 1\nxx", want: "batch terminator"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			_, err := readGitBlob(bufio.NewReader(strings.NewReader(test.data)), objectID)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("readGitBlob() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestArchiveReadersRejectNoncanonicalInputs(t *testing.T) {
	t.Parallel()
	epoch := time.Date(2026, 8, 5, 1, 2, 3, 0, time.UTC)
	if _, err := readArchive(t.Context(), nil, false, epoch); err == nil {
		t.Fatal("readArchive(empty) succeeded")
	}
	if _, err := readArchive(t.Context(), []byte("not gzip"), false, epoch); err == nil ||
		!strings.Contains(err.Error(), "gzip") {
		t.Fatalf("readArchive(invalid gzip) error = %v", err)
	}
	if _, err := readArchive(t.Context(), []byte("not zip"), true, epoch); err == nil ||
		!strings.Contains(err.Error(), "ZIP") {
		t.Fatalf("readArchive(invalid ZIP) error = %v", err)
	}

	zipCases := []struct {
		name   string
		modify func(*zip.FileHeader)
		want   string
	}{
		{name: "unsafe path", modify: func(header *zip.FileHeader) { header.Name = "../file" }, want: "unsafe"},
		{name: "stored", modify: func(header *zip.FileHeader) { header.Method = zip.Store }, want: "metadata"},
		{name: "timestamp", modify: func(header *zip.FileHeader) { header.Modified = epoch.Add(time.Second) }, want: "metadata"},
	}
	for _, test := range zipCases {
		t.Run("zip "+test.name, func(t *testing.T) {
			t.Parallel()
			archive := malformedZip(t, epoch, test.modify)
			_, err := readArchive(t.Context(), archive, true, epoch)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("readArchive() error = %v, want containing %q", err, test.want)
			}
		})
	}

	tarCases := []struct {
		name     string
		typeflag byte
		link     string
		mutate   func(*tar.Header)
		want     string
	}{
		{name: "timestamp", typeflag: tar.TypeReg, mutate: func(header *tar.Header) { header.ModTime = epoch.Add(time.Second) }, want: "metadata"},
		{name: "invalid mode", typeflag: tar.TypeReg, mutate: func(header *tar.Header) { header.Mode = 0o1777 }, want: "invalid mode"},
		{name: "unsupported type", typeflag: tar.TypeDir, want: "unsupported type"},
		{name: "unsafe symlink", typeflag: tar.TypeSymlink, link: "../../outside", want: "unsafe metadata"},
	}
	for _, test := range tarCases {
		t.Run("tar "+test.name, func(t *testing.T) {
			t.Parallel()
			archive := malformedTar(t, epoch, test.typeflag, test.link, test.mutate)
			_, err := readArchive(t.Context(), archive, false, epoch)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("readArchive() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestDirectorySignatureAndSBOMFailurePaths(t *testing.T) {
	t.Parallel()
	if err := validateDirectory(filepath.Join(t.TempDir(), "missing"), nil); err == nil {
		t.Fatal("validateDirectory(missing) succeeded")
	}
	directory := t.TempDir()
	if err := os.Mkdir(filepath.Join(directory, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := validateDirectory(directory, []string{"nested"}); err == nil ||
		!strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("validateDirectory(directory asset) error = %v", err)
	}

	publicKey, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	checksums := []byte("checksums\n")
	writeTestFile(t, filepath.Join(directory, "checksums.txt.pem"), encodeTestPublicKey(t, publicKey))
	writeTestFile(t, filepath.Join(directory, "checksums.txt.sig"), []byte("short"))
	if err := authenticateChecksums(directory, checksums, publicKey); err == nil ||
		!strings.Contains(err.Error(), "signature length") {
		t.Fatalf("authenticateChecksums(short signature) error = %v", err)
	}

	for _, data := range [][]byte{
		[]byte("{"),
		[]byte("{\"unknown\":true}"),
		[]byte("{} {}"),
	} {
		if err := verifySBOM(data, nil, testVersion, time.Unix(1, 0)); err == nil {
			t.Fatalf("verifySBOM(%q) succeeded", data)
		}
	}
}

func TestDirectoryAndBinaryArchiveRequireExactTrustedContents(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	writeTestFile(t, filepath.Join(directory, "artifact"), []byte("data"))
	if err := validateDirectory(directory, []string{"artifact"}); err != nil {
		t.Fatalf("validateDirectory(valid) error = %v", err)
	}
	if err := validateDirectory(directory, []string{"different"}); err == nil ||
		!strings.Contains(err.Error(), "artifact set") {
		t.Fatalf("validateDirectory(mismatch) error = %v", err)
	}
	target := releaseTarget{goos: "linux", goarch: "amd64"}
	if err := verifyBinaryArchive(
		t.Context(), nil, target, "0.1.0", testVersion, nil, nil, nil,
	); err == nil || !strings.Contains(err.Error(), "require 3") {
		t.Fatalf("verifyBinaryArchive(empty) error = %v", err)
	}
	base := strings.TrimSuffix(target.archiveName("0.1.0"), ".tar.gz")
	entries := []archiveEntry{
		{name: base + "/spice", mode: 0o755, data: []byte("not a Go binary")},
		{name: base + "/LICENSE", mode: 0o644, data: []byte("wrong")},
		{name: base + "/README.md", mode: 0o644, data: []byte("readme")},
	}
	if err := verifyBinaryArchive(
		t.Context(), entries, target, "0.1.0", testVersion,
		[]byte("license"), []byte("readme"), nil,
	); err == nil || !strings.Contains(err.Error(), "trusted release source") {
		t.Fatalf("verifyBinaryArchive(payload mismatch) error = %v", err)
	}
}

func TestStopGitCommandPreservesCause(t *testing.T) {
	t.Parallel()
	command := exec.Command(os.Args[0], "-test.run=TestStopGitCommandHelperProcess")
	command.Env = append(os.Environ(), "SPICE_RELEASEVERIFY_HELPER=1")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	cause := errors.New("sentinel verification failure")
	if err := stopGitCommand(command, cause); !errors.Is(err, cause) {
		t.Fatalf("stopGitCommand() error = %v", err)
	}
}

func TestStopGitCommandHelperProcess(t *testing.T) {
	if os.Getenv("SPICE_RELEASEVERIFY_HELPER") != "1" {
		return
	}
	select {}
}

func malformedZip(
	t *testing.T,
	epoch time.Time,
	modify func(*zip.FileHeader),
) []byte {
	t.Helper()
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	header := &zip.FileHeader{Name: "release/file", Method: zip.Deflate, Modified: epoch}
	header.SetMode(0o644)
	modify(header)
	target, err := writer.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := target.Write([]byte("data")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func malformedTar(
	t *testing.T,
	epoch time.Time,
	typeflag byte,
	link string,
	modify func(*tar.Header),
) []byte {
	t.Helper()
	var output bytes.Buffer
	gzipWriter, err := gzip.NewWriterLevel(&output, gzip.BestCompression)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter.ModTime = epoch
	gzipWriter.OS = 255
	tarWriter := tar.NewWriter(gzipWriter)
	header := &tar.Header{
		Name: "release/file", Mode: 0o644, Size: 4, Typeflag: typeflag,
		Linkname: link, ModTime: epoch, AccessTime: epoch, ChangeTime: epoch,
		Format: tar.FormatPAX,
	}
	if typeflag == tar.TypeSymlink || typeflag == tar.TypeDir {
		header.Size = 0
	}
	if modify != nil {
		modify(header)
	}
	if err := tarWriter.WriteHeader(header); err != nil {
		t.Fatal(err)
	}
	if header.Size != 0 {
		if _, err := tarWriter.Write([]byte("data")); err != nil {
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

func TestSourceAndBoundedHelpers(t *testing.T) {
	t.Parallel()
	entries := []sourceEntry{{path: "file", data: []byte("data")}}
	if _, err := trustedRegularFile(entries, "missing"); err == nil {
		t.Fatal("trustedRegularFile(missing) succeeded")
	}
	if got, err := trustedRegularFile(entries, "file"); err != nil || string(got) != "data" {
		t.Fatalf("trustedRegularFile(valid) = %q, %v", got, err)
	}
	if safeLinkTarget("dir/link", "") || safeLinkTarget("dir/link", "../../outside") ||
		!safeLinkTarget("dir/link", "../file") {
		t.Fatal("safeLinkTarget contracts failed")
	}
	long := bytes.Repeat([]byte{'x'}, maxGitDiagnosticBytes+1)
	if len(bounded(long)) != maxGitDiagnosticBytes || bounded([]byte(" x \n")) != "x" {
		t.Fatal("bounded() did not trim and cap diagnostics")
	}
	if portableVolumeName("C:/file") != "C:" || portableVolumeName("plain") != "" {
		t.Fatal("portableVolumeName() mismatch")
	}
}

func TestReadBoundedRegularFileFailures(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	if _, err := readBoundedRegularFile(directory, "missing", 10); err == nil {
		t.Fatal("readBoundedRegularFile(missing) succeeded")
	}
	writeTestFile(t, filepath.Join(directory, "large"), []byte("12345"))
	if _, err := readBoundedRegularFile(directory, "large", 4); err == nil {
		t.Fatal("readBoundedRegularFile(large) succeeded")
	}
}
