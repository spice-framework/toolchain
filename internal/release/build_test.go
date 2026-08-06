package release

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
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

func TestBuildProducesReproducibleSignedHostRelease(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	epoch := time.Unix(1_788_000_000, 0).UTC()
	seed := make([]byte, ed25519.SeedSize)
	for index := range seed {
		seed[index] = byte(index + 1)
	}
	keyText := []byte(base64.StdEncoding.EncodeToString(seed))
	parent := t.TempDir()
	config := Config{
		Root:       root,
		OutputDir:  filepath.Join(parent, "first"),
		Version:    "v0.9.0-rc.1",
		Epoch:      epoch,
		Targets:    []Target{HostTarget()},
		PrivateKey: keyText,
	}
	first, err := Build(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOAMD64", "v3")
	t.Setenv("GOARM64", "v9.4")
	t.Setenv("GO111MODULE", "off")
	t.Setenv("GOEXPERIMENT", "definitely-invalid")
	t.Setenv("GOFIPS140", "latest")
	t.Setenv("GODEBUG", "gocacheverify=1")
	config.OutputDir = filepath.Join(parent, "second")
	second, err := Build(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(first.Files, second.Files) {
		t.Fatalf("release files differ: %v != %v", first.Files, second.Files)
	}
	for _, name := range first.Files {
		left, err := os.ReadFile(filepath.Join(first.OutputDir, name))
		if err != nil {
			t.Fatal(err)
		}
		right, err := os.ReadFile(filepath.Join(second.OutputDir, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(left) != string(right) {
			t.Errorf("release artifact %q is not reproducible", name)
		}
	}

	checkSignedChecksums(t, first.OutputDir)
	checkSBOM(t, first.OutputDir, root, epoch)
	checkHostArchive(t, first.OutputDir, first.Files)
	if _, err := Build(t.Context(), config); err == nil {
		t.Fatal("Build() overwrote an existing output directory")
	}
}

func TestBuildRejectsUnsafeOrIncompleteConfiguration(t *testing.T) {
	t.Parallel()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	base := Config{
		Root:          root,
		OutputDir:     filepath.Join(t.TempDir(), "release"),
		Version:       "v0.1.0",
		Epoch:         time.Unix(1, 0),
		Targets:       []Target{HostTarget()},
		AllowUnsigned: true,
	}
	tests := []struct {
		name   string
		change func(*Config)
	}{
		{
			name:   "nil context",
			change: func(*Config) {},
		},
		{
			name: "invalid version",
			change: func(config *Config) {
				config.Version = "1.0"
			},
		},
		{
			name: "noncanonical version",
			change: func(config *Config) {
				config.Version = "v1.0"
			},
		},
		{
			name: "missing epoch",
			change: func(config *Config) {
				config.Epoch = time.Time{}
			},
		},
		{
			name: "unsigned is not explicit",
			change: func(config *Config) {
				config.AllowUnsigned = false
			},
		},
		{
			name: "signed rehearsal",
			change: func(config *Config) {
				config.PrivateKey = []byte("not-used")
			},
		},
		{
			name: "unsupported target",
			change: func(config *Config) {
				config.Targets = []Target{{GOOS: "plan9", GOARCH: "amd64"}}
			},
		},
		{
			name: "duplicate target",
			change: func(config *Config) {
				config.Targets = []Target{HostTarget(), HostTarget()}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			config := base
			config.OutputDir += "-" + strings.ReplaceAll(test.name, " ", "-")
			test.change(&config)
			ctx := t.Context()
			if test.name == "nil context" {
				ctx = nil
			}
			if _, err := Build(ctx, config); err == nil {
				t.Fatal("Build() error = nil")
			}
		})
	}
}

func TestBuildRejectsWrongGoBeforeReadingGit(t *testing.T) {
	fakeRoot := t.TempDir()
	source := filepath.Join(fakeRoot, "main.go")
	if err := os.WriteFile(source, []byte(`package main

import "fmt"

func main() { fmt.Print("go1.25.0") }
`), 0o600); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(fakeRoot, "go")
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	command := exec.CommandContext(t.Context(), "go", "build", "-o", executable, source)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build fake Go executable: %v: %s", err, output)
	}
	t.Setenv("PATH", fakeRoot)
	_, err := Build(t.Context(), Config{
		Root:          t.TempDir(),
		OutputDir:     filepath.Join(t.TempDir(), "release"),
		Version:       "v0.1.0",
		Epoch:         time.Unix(1, 0),
		Targets:       []Target{HostTarget()},
		AllowUnsigned: true,
	})
	if err == nil || !strings.Contains(err.Error(), "require go1.26.5, got \"go1.25.0\"") {
		t.Fatalf("Build() wrong Go error = %v", err)
	}
}

func TestArchiveFormatsAreDeterministic(t *testing.T) {
	t.Parallel()
	epoch := time.Unix(1_788_000_000, 0).UTC()
	entries := []archiveEntry{
		{name: "spice/spice", mode: 0o755, data: []byte("binary")},
		{name: "spice/LICENSE", mode: 0o644, data: []byte("license")},
	}
	for _, target := range []Target{
		{GOOS: "linux", GOARCH: "amd64"},
		{GOOS: "windows", GOARCH: "amd64"},
	} {
		t.Run(target.GOOS, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			first := filepath.Join(root, "first"+target.ArchiveExtension())
			second := filepath.Join(root, "second"+target.ArchiveExtension())
			if err := writeArchive(first, target, epoch, entries); err != nil {
				t.Fatal(err)
			}
			if err := writeArchive(second, target, epoch, entries); err != nil {
				t.Fatal(err)
			}
			left, err := os.ReadFile(first)
			if err != nil {
				t.Fatal(err)
			}
			right, err := os.ReadFile(second)
			if err != nil {
				t.Fatal(err)
			}
			if string(left) != string(right) {
				t.Fatal("archive bytes changed across identical writes")
			}
			decoded := readArchive(t, first, target)
			if string(decoded["spice"]) != "binary" ||
				string(decoded["LICENSE"]) != "license" {
				t.Fatalf("archive entries = %#v", decoded)
			}
		})
	}
}

func checkSignedChecksums(t *testing.T, directory string) {
	t.Helper()
	checksums, err := os.ReadFile(filepath.Join(directory, "checksums.txt"))
	if err != nil {
		t.Fatal(err)
	}
	signatureText, err := os.ReadFile(
		filepath.Join(directory, "checksums.txt.sig"),
	)
	if err != nil {
		t.Fatal(err)
	}
	publicPEM, err := os.ReadFile(
		filepath.Join(directory, "checksums.txt.pem"),
	)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(publicPEM)
	if block == nil {
		t.Fatal("public key is not PEM")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, ok := parsed.(ed25519.PublicKey)
	if !ok || !ed25519.Verify(publicKey, checksums, signatureText) {
		t.Fatal("release checksum signature did not verify")
	}
	for line := range strings.SplitSeq(
		strings.TrimSpace(string(checksums)),
		"\n",
	) {
		parts := strings.SplitN(line, "  ", 2)
		if len(parts) != 2 {
			t.Fatalf("invalid checksum line %q", line)
		}
		if _, err := os.Stat(filepath.Join(directory, parts[1])); err != nil {
			t.Fatalf("checksum target %q: %v", parts[1], err)
		}
		data, err := os.ReadFile(filepath.Join(directory, parts[1]))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != parts[0] {
			t.Fatalf("checksum mismatch for %q", parts[1])
		}
	}
}

func checkSBOM(
	t *testing.T,
	directory string,
	root string,
	epoch time.Time,
) {
	t.Helper()
	matches, err := filepath.Glob(
		filepath.Join(directory, "*_sbom.spdx.json"),
	)
	if err != nil || len(matches) != 1 {
		t.Fatalf("SBOM files = %v, %v", matches, err)
	}
	data, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatal(err)
	}
	var document spdxDocument
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if document.SPDXVersion != "SPDX-2.3" ||
		document.CreationInfo.Created != epoch.Format(time.RFC3339) ||
		len(document.Packages) < 2 ||
		len(document.Relationships) != len(document.Packages)-1 {
		t.Fatalf("SBOM = %#v", document)
	}
	if !strings.HasPrefix(
		document.DocumentNamespace,
		"https://github.com/spice-framework/toolchain/releases/",
	) {
		t.Fatalf("SBOM namespace = %q", document.DocumentNamespace)
	}
	if strings.Contains(string(data), root) ||
		strings.Contains(string(data), filepath.ToSlash(root)) {
		t.Fatal("SBOM contains an absolute workspace path")
	}
}

func checkHostArchive(t *testing.T, directory string, files []string) {
	t.Helper()
	target := HostTarget()
	var archiveName string
	for _, name := range files {
		if strings.Contains(
			name,
			"_"+target.GOOS+"_"+target.GOARCH,
		) && strings.HasSuffix(name, target.ArchiveExtension()) {
			archiveName = name
			break
		}
	}
	if archiveName == "" {
		t.Fatal("host archive was not created")
	}
	entries := readArchive(t, filepath.Join(directory, archiveName), target)
	for _, name := range []string{
		target.ExecutableName(),
		"LICENSE",
		"README.md",
	} {
		if _, found := entries[name]; !found {
			t.Errorf("archive is missing %s", name)
		}
	}
	binary := filepath.Join(t.TempDir(), target.ExecutableName())
	if err := os.WriteFile(binary, entries[target.ExecutableName()], 0o755); err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(binary, "version").CombinedOutput()
	if err != nil {
		t.Fatalf("run released CLI: %v: %s", err, output)
	}
	if strings.TrimSpace(string(output)) != "spice v0.9.0-rc.1" {
		t.Fatalf("released CLI version = %q", output)
	}
}

func readArchive(
	t *testing.T,
	filename string,
	target Target,
) map[string][]byte {
	t.Helper()
	if target.GOOS == "windows" {
		return readZip(t, filename)
	}
	return readTarGzip(t, filename)
}

func readZip(t *testing.T, filename string) map[string][]byte {
	t.Helper()
	reader, err := zip.OpenReader(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := reader.Close(); err != nil {
			t.Errorf("close ZIP: %v", err)
		}
	}()
	entries := make(map[string][]byte)
	for _, file := range reader.File {
		source, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, readErr := io.ReadAll(source)
		closeErr := source.Close()
		if readErr != nil || closeErr != nil {
			t.Fatalf("read ZIP entry: %v, close: %v", readErr, closeErr)
		}
		entries[filepath.Base(file.Name)] = data
	}
	return entries
}

func readTarGzip(t *testing.T, filename string) map[string][]byte {
	t.Helper()
	file, err := os.Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			t.Errorf("close archive: %v", closeErr)
		}
	}()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := gzipReader.Close(); err != nil {
			t.Errorf("close gzip: %v", err)
		}
	}()
	reader := tar.NewReader(gzipReader)
	entries := make(map[string][]byte)
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
		entries[filepath.Base(header.Name)] = data
	}
	return entries
}
