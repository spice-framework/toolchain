// Package releaseverify independently verifies a complete Spice release.
//
// It deliberately does not share release-builder validation code. Verification
// is anchored in a caller-supplied public key and an exact commit in a trusted
// checkout, rather than metadata shipped beside the artifacts.
package releaseverify

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

const (
	maxControlFileBytes = 1 << 20
	maxArtifactBytes    = 512 << 20
	maxPublicKeyBytes   = 64 << 10
	modulePath          = "github.com/spice-framework/toolchain"
)

// Config identifies the untrusted artifacts and the trusted release inputs.
type Config struct {
	Directory        string
	Version          string
	Repository       string
	Commit           string
	TrustedPublicKey []byte
}

// Result summarizes a successfully verified release.
type Result struct {
	Files  []string
	Commit string
	Epoch  time.Time
}

// Verify authenticates and structurally validates one complete release.
func Verify(ctx context.Context, config Config) (Result, error) {
	if ctx == nil {
		return Result{}, errors.New("verify release: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("verify release: %w", err)
	}
	if !semver.IsValid(config.Version) || semver.Canonical(config.Version) != config.Version {
		return Result{}, fmt.Errorf(
			"verify release: version %q is not canonical semantic version",
			config.Version,
		)
	}
	if config.Directory == "" || config.Repository == "" {
		return Result{}, errors.New(
			"verify release: artifact directory and trusted repository are required",
		)
	}
	if err := validateExactCommit(config.Commit); err != nil {
		return Result{}, err
	}
	trustedKey, err := parsePublicKey(config.TrustedPublicKey, "trusted public key")
	if err != nil {
		return Result{}, err
	}
	directory, err := filepath.Abs(config.Directory)
	if err != nil {
		return Result{}, fmt.Errorf("resolve release artifact directory: %w", err)
	}
	repository, err := filepath.Abs(config.Repository)
	if err != nil {
		return Result{}, fmt.Errorf("resolve trusted repository: %w", err)
	}
	expectedAssets, checksummedAssets := expectedAssetNames(config.Version)
	if directoryErr := validateDirectory(directory, expectedAssets); directoryErr != nil {
		return Result{}, directoryErr
	}
	checksums, err := readBoundedRegularFile(
		directory,
		"checksums.txt",
		maxControlFileBytes,
	)
	if err != nil {
		return Result{}, err
	}
	if authenticationErr := authenticateChecksums(directory, checksums, trustedKey); authenticationErr != nil {
		return Result{}, authenticationErr
	}
	digests, checksumErr := parseChecksums(checksums, checksummedAssets)
	if checksumErr != nil {
		return Result{}, checksumErr
	}
	epoch, expectedSource, err := trustedSource(
		ctx,
		repository,
		config.Commit,
	)
	if err != nil {
		return Result{}, err
	}
	license, err := trustedRegularFile(expectedSource, "LICENSE")
	if err != nil {
		return Result{}, err
	}
	readme, err := trustedRegularFile(expectedSource, "README.md")
	if err != nil {
		return Result{}, err
	}
	versionName := strings.TrimPrefix(config.Version, "v")
	sourceName := "spice_" + versionName + "_source.tar.gz"
	sourceData, err := readAuthenticatedArtifact(
		ctx,
		directory,
		sourceName,
		digests[sourceName],
	)
	if err != nil {
		return Result{}, err
	}
	sourceEntries, err := readArchive(ctx, sourceData, false, epoch)
	if err != nil {
		return Result{}, fmt.Errorf("verify %s: %w", sourceName, err)
	}
	sourceFiles, err := verifySourceArchive(
		sourceEntries,
		"spice-"+versionName,
		expectedSource,
	)
	if err != nil {
		return Result{}, fmt.Errorf("verify %s: %w", sourceName, err)
	}
	modules, err := sourceModules(sourceFiles)
	if err != nil {
		return Result{}, err
	}
	if binaryErr := verifyBinaryArtifacts(
		ctx,
		directory,
		digests,
		versionName,
		config.Version,
		epoch,
		license,
		readme,
		expectedSource,
	); binaryErr != nil {
		return Result{}, binaryErr
	}
	sbomName := "spice_" + versionName + "_sbom.spdx.json"
	sbom, err := readAuthenticatedArtifact(
		ctx,
		directory,
		sbomName,
		digests[sbomName],
	)
	if err != nil {
		return Result{}, err
	}
	if int64(len(sbom)) > maxControlFileBytes {
		return Result{}, fmt.Errorf("verify %s: exceeds control-file size limit", sbomName)
	}
	if err := verifySBOM(sbom, modules, config.Version, epoch); err != nil {
		return Result{}, fmt.Errorf("verify %s: %w", sbomName, err)
	}
	return Result{
		Files:  slices.Clone(expectedAssets),
		Commit: strings.ToLower(config.Commit),
		Epoch:  epoch,
	}, nil
}

func expectedAssetNames(version string) ([]string, []string) {
	versionName := strings.TrimPrefix(version, "v")
	checksummed := make([]string, 0, 8)
	for _, target := range releaseTargets() {
		checksummed = append(checksummed, target.archiveName(versionName))
	}
	checksummed = append(
		checksummed,
		"spice_"+versionName+"_source.tar.gz",
		"spice_"+versionName+"_sbom.spdx.json",
	)
	slices.Sort(checksummed)
	assets := append([]string(nil), checksummed...)
	assets = append(
		assets,
		"checksums.txt",
		"checksums.txt.pem",
		"checksums.txt.sig",
	)
	slices.Sort(assets)
	return assets, checksummed
}

func validateDirectory(directory string, expected []string) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("read release artifact directory: %w", err)
	}
	actual := make([]string, 0, len(entries))
	for _, entry := range entries {
		info, infoErr := entry.Info()
		if infoErr != nil {
			return fmt.Errorf("inspect release artifact %q: %w", entry.Name(), infoErr)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("release artifact %q is not a regular file", entry.Name())
		}
		if info.Size() < 0 || info.Size() > maxArtifactBytes {
			return fmt.Errorf(
				"release artifact %q exceeds %d bytes",
				entry.Name(),
				maxArtifactBytes,
			)
		}
		actual = append(actual, entry.Name())
	}
	slices.Sort(actual)
	if !slices.Equal(actual, expected) {
		return fmt.Errorf(
			"release artifact set is %v, require exactly %v",
			actual,
			expected,
		)
	}
	return nil
}

func authenticateChecksums(
	directory string,
	checksums []byte,
	trustedKey ed25519.PublicKey,
) error {
	emittedPEM, err := readBoundedRegularFile(
		directory,
		"checksums.txt.pem",
		maxPublicKeyBytes,
	)
	if err != nil {
		return err
	}
	emittedKey, err := parsePEMPublicKey(emittedPEM, "emitted public key")
	if err != nil {
		return err
	}
	if !bytes.Equal(emittedKey, trustedKey) {
		return errors.New("emitted release public key does not match trusted public key")
	}
	signature, err := readBoundedRegularFile(
		directory,
		"checksums.txt.sig",
		ed25519.SignatureSize,
	)
	if err != nil {
		return err
	}
	if len(signature) != ed25519.SignatureSize {
		return fmt.Errorf(
			"release signature length is %d, require %d",
			len(signature),
			ed25519.SignatureSize,
		)
	}
	if !ed25519.Verify(trustedKey, checksums, signature) {
		return errors.New("release checksum signature is invalid")
	}
	return nil
}

func parsePublicKey(data []byte, label string) (ed25519.PublicKey, error) {
	return parsePEMPublicKey(data, label)
}

func parsePEMPublicKey(data []byte, label string) (ed25519.PublicKey, error) {
	if len(data) == 0 || len(data) > maxPublicKeyBytes {
		return nil, fmt.Errorf("parse %s: invalid size", label)
	}
	block, rest := pem.Decode(data)
	if block == nil || block.Type != "PUBLIC KEY" || len(block.Headers) != 0 ||
		len(bytes.TrimSpace(rest)) != 0 {
		return nil, fmt.Errorf("parse %s: require one PUBLIC KEY PEM block", label)
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", label, err)
	}
	key, ok := parsed.(ed25519.PublicKey)
	if !ok || len(key) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("parse %s: require Ed25519 public key", label)
	}
	canonicalDER, err := x509.MarshalPKIXPublicKey(key)
	if err != nil {
		return nil, fmt.Errorf("parse %s: encode canonical key: %w", label, err)
	}
	canonicalPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: canonicalDER})
	if !bytes.Equal(data, canonicalPEM) {
		return nil, fmt.Errorf("parse %s: PEM is not canonical", label)
	}
	return append(ed25519.PublicKey(nil), key...), nil
}

func parseChecksums(
	data []byte,
	expected []string,
) (map[string][sha256.Size]byte, error) {
	if len(data) == 0 || data[len(data)-1] != '\n' || bytes.ContainsRune(data, '\r') {
		return nil, errors.New("checksums.txt must use canonical LF-terminated lines")
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != len(expected) {
		return nil, fmt.Errorf("checksums.txt has %d lines, require %d", len(lines), len(expected))
	}
	digests := make(map[string][sha256.Size]byte, len(lines))
	for index, line := range lines {
		digest, name, found := strings.Cut(line, "  ")
		if !found || strings.Contains(name, "  ") || name != expected[index] ||
			len(digest) != sha256.Size*2 || digest != strings.ToLower(digest) {
			return nil, fmt.Errorf("checksums.txt line %d is not canonical", index+1)
		}
		decoded, err := hex.DecodeString(digest)
		if err != nil || len(decoded) != sha256.Size {
			return nil, fmt.Errorf("checksums.txt line %d has invalid SHA-256", index+1)
		}
		digests[name] = [sha256.Size]byte(decoded)
	}
	return digests, nil
}

func readAuthenticatedArtifact(
	ctx context.Context,
	directory string,
	name string,
	expected [sha256.Size]byte,
) (result []byte, resultErr error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, fmt.Errorf("open release artifact root: %w", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, root.Close())
	}()
	file, err := root.Open(name)
	if err != nil {
		return nil, fmt.Errorf("open release artifact %q: %w", name, err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, file.Close())
	}()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > maxArtifactBytes {
		return nil, fmt.Errorf("release artifact %q is not a bounded regular file", name)
	}
	data := make([]byte, 0, int(info.Size()))
	buffer := make([]byte, 64<<10)
	for {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("read release artifact %q: %w", name, err)
		}
		count, readErr := file.Read(buffer)
		if count > 0 {
			data = append(data, buffer[:count]...)
			if int64(len(data)) > maxArtifactBytes {
				return nil, fmt.Errorf("release artifact %q exceeds %d bytes", name, maxArtifactBytes)
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("read release artifact %q: %w", name, readErr)
		}
	}
	actual := sha256.Sum256(data)
	if actual != expected {
		return nil, fmt.Errorf("release artifact %q has a SHA-256 mismatch", name)
	}
	return data, nil
}

func readBoundedRegularFile(
	directory string,
	name string,
	maximum int64,
) (result []byte, resultErr error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, fmt.Errorf("open release artifact root: %w", err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, root.Close())
	}()
	file, err := root.Open(name)
	if err != nil {
		return nil, fmt.Errorf("open release artifact %q: %w", name, err)
	}
	defer func() {
		resultErr = errors.Join(resultErr, file.Close())
	}()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect release artifact %q: %w", name, err)
	}
	if !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > maximum {
		return nil, fmt.Errorf(
			"release artifact %q is not a regular file bounded to %d bytes",
			name,
			maximum,
		)
	}
	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, fmt.Errorf("read release artifact %q: %w", name, err)
	}
	if int64(len(data)) > maximum {
		return nil, fmt.Errorf("release artifact %q exceeds %d bytes", name, maximum)
	}
	return data, nil
}
