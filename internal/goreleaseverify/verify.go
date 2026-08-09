package goreleaseverify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Verify checks independent policy, exact Git identity, authenticated module
// and vendor state, deterministic artifacts, and an isolated trimpath build.
func Verify(ctx context.Context, config Config) (Result, error) {
	if ctx == nil {
		return Result{}, errors.New("verify Go module release: context is nil")
	}
	runner, err := newSystemGoRunner()
	if err != nil {
		return Result{}, err
	}
	return verify(ctx, config, runner)
}

func verify(ctx context.Context, config Config, runner goRunner) (_ Result, resultErr error) {
	if ctx == nil {
		return Result{}, errors.New("verify Go module release: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("verify Go module release: %w", err)
	}
	policy, err := selectPolicy(config)
	if err != nil {
		return Result{}, err
	}
	source, err := trustedSource(ctx, config, policy)
	if err != nil {
		return Result{}, err
	}
	modules, err := validateCommittedModule(ctx, source, policy)
	if err != nil {
		return Result{}, err
	}
	files := expectedAssetNames(policy)
	artifacts, directory, err := readArtifactSet(config.Directory, files)
	if err != nil {
		return Result{}, err
	}
	base := policy.repository + "_" + strings.TrimPrefix(policy.version, "v")
	archiveName := base + "_source.tar.gz"
	sbomName := base + "_sbom.spdx.json"
	metadataName := base + "_release.json"
	checksummed := []string{metadataName, sbomName, archiveName}
	slices.Sort(checksummed)
	if checksumErr := verifyChecksums(artifacts, checksummed); checksumErr != nil {
		return Result{}, checksumErr
	}
	expectedArchive, err := expectedSourceArchive(ctx, source, policy.repository, policy.version)
	if err != nil {
		return Result{}, err
	}
	if !bytes.Equal(artifacts[archiveName], expectedArchive) {
		return Result{}, errors.New("go module source archive is not byte-reproducible from the trusted commit")
	}
	workspace, err := materializeSourceArchive(artifacts[archiveName], source, policy)
	if err != nil {
		return Result{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, workspace.Close()) }()
	if len(policy.requiredModules) == 0 {
		if graphErr := verifyDependencyFreeAndBuild(ctx, workspace, policy, runner); graphErr != nil {
			return Result{}, graphErr
		}
	} else if authenticationErr := authenticateVendorAndBuild(ctx, workspace, source, runner); authenticationErr != nil {
		return Result{}, authenticationErr
	}
	expectedSBOMBytes, err := marshalCanonical(expectedSBOM(policy, source.commit, source.epoch, modules))
	if err != nil {
		return Result{}, err
	}
	if sbomErr := validateCanonicalJSON(artifacts[sbomName], expectedSBOMBytes, "SPDX SBOM"); sbomErr != nil {
		return Result{}, sbomErr
	}
	metadataArtifacts := []artifactFact{
		artifactFactFor(sbomName, artifacts[sbomName]),
		artifactFactFor(archiveName, artifacts[archiveName]),
	}
	slices.SortFunc(metadataArtifacts, func(left, right artifactFact) int {
		return strings.Compare(left.Name, right.Name)
	})
	expectedMetadata := releaseMetadata{
		Schema: artifactSchema, Profile: ProfileGoModule, Repository: policy.repository,
		Module: policy.module, Source: policy.source, Version: policy.version,
		Commit: source.commit, SourceDateEpoch: source.epoch.Unix(), Go: "1.26.5",
		Artifacts: metadataArtifacts,
	}
	expectedMetadataBytes, err := marshalCanonical(expectedMetadata)
	if err != nil {
		return Result{}, err
	}
	if err := validateCanonicalJSON(artifacts[metadataName], expectedMetadataBytes, "release metadata"); err != nil {
		return Result{}, err
	}
	if err := revalidateSource(ctx, source, policy.version); err != nil {
		return Result{}, err
	}
	if err := revalidateArtifactSet(directory, artifacts, files); err != nil {
		return Result{}, err
	}
	if _, err := writeVerifiedOutput(config.VerifiedOutput, artifacts, files); err != nil {
		return Result{}, err
	}
	return Result{
		Files: slices.Clone(files), Commit: source.commit, Epoch: source.epoch,
		Module: policy.module, Profile: ProfileGoModule,
	}, nil
}

func selectPolicy(config Config) (releasePolicy, error) {
	if config.Profile != ProfileGoModule {
		return releasePolicy{}, fmt.Errorf("release profile %q is unsupported; require %s", config.Profile, ProfileGoModule)
	}
	if strings.HasPrefix(config.RepositoryName, "starter-") {
		return releasePolicy{}, errors.New("starter repositories must use the key-backed library release verifier")
	}
	policy, found := releasePolicies[config.RepositoryName]
	if !found {
		return releasePolicy{}, fmt.Errorf("repository %q is not independently authorized for %s", config.RepositoryName, ProfileGoModule)
	}
	if config.Module != policy.module || config.CanonicalSource != policy.source ||
		config.Version != policy.version {
		return releasePolicy{}, errors.New("trusted release inputs do not match independent module policy")
	}
	return policy, nil
}

func expectedAssetNames(policy releasePolicy) []string {
	base := policy.repository + "_" + strings.TrimPrefix(policy.version, "v")
	result := []string{
		"checksums.txt", base + "_release.json", base + "_sbom.spdx.json", base + "_source.tar.gz",
	}
	slices.Sort(result)
	return result
}

func readArtifactSet(configured string, expected []string) (map[string][]byte, string, error) {
	directory, err := realDirectory(configured, "artifact directory")
	if err != nil {
		return nil, "", err
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, "", fmt.Errorf("read Go module artifact directory: %w", err)
	}
	actual := make([]string, 0, len(entries))
	for _, entry := range entries {
		info, infoErr := entry.Info()
		if infoErr != nil {
			return nil, "", fmt.Errorf("inspect Go module artifact %q: %w", entry.Name(), infoErr)
		}
		if !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > maxArtifactBytes {
			return nil, "", fmt.Errorf("go module artifact %q is not a bounded regular file", entry.Name())
		}
		actual = append(actual, entry.Name())
	}
	slices.Sort(actual)
	if !slices.Equal(actual, expected) {
		return nil, "", fmt.Errorf("go module artifact set is %v, require exactly %v", actual, expected)
	}
	result := make(map[string][]byte, len(expected))
	for _, name := range expected {
		maximum := int64(maxArtifactBytes)
		switch {
		case name == "checksums.txt":
			maximum = maxChecksums
		case strings.HasSuffix(name, "_release.json"), strings.HasSuffix(name, "_sbom.spdx.json"):
			maximum = maxControlFile
		}
		content, err := readBoundedRegularFile(directory, name, maximum)
		if err != nil {
			return nil, "", err
		}
		result[name] = content
	}
	return result, directory, nil
}

func readBoundedRegularFile(directory, name string, maximum int64) (result []byte, resultErr error) {
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, fmt.Errorf("open Go module artifact root: %w", err)
	}
	defer func() { resultErr = errors.Join(resultErr, root.Close()) }()
	info, err := root.Lstat(name)
	if err != nil {
		return nil, fmt.Errorf("inspect Go module artifact %q: %w", name, err)
	}
	if !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > maximum {
		return nil, fmt.Errorf("go module artifact %q is not a regular file bounded to %d bytes", name, maximum)
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, fmt.Errorf("open Go module artifact %q: %w", name, err)
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	opened, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect opened Go module artifact %q: %w", name, err)
	}
	if !opened.Mode().IsRegular() || opened.Size() < 0 || opened.Size() > maximum || !os.SameFile(info, opened) {
		return nil, fmt.Errorf("go module artifact %q changed while it was opened", name)
	}
	data, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, fmt.Errorf("read Go module artifact %q: %w", name, err)
	}
	if int64(len(data)) > maximum {
		return nil, fmt.Errorf("go module artifact %q exceeds %d bytes", name, maximum)
	}
	return data, nil
}

func verifyChecksums(artifacts map[string][]byte, expected []string) error {
	checksums := artifacts["checksums.txt"]
	if len(checksums) == 0 || checksums[len(checksums)-1] != '\n' || bytes.ContainsRune(checksums, '\r') {
		return errors.New("checksums.txt must use canonical LF-terminated lines")
	}
	lines := strings.Split(strings.TrimSuffix(string(checksums), "\n"), "\n")
	if len(lines) != len(expected) {
		return fmt.Errorf("checksums.txt has %d lines, require %d", len(lines), len(expected))
	}
	for index, line := range lines {
		digestText, name, found := strings.Cut(line, "  ")
		if !found || strings.Contains(name, "  ") || name != expected[index] ||
			len(digestText) != sha256.Size*2 || digestText != strings.ToLower(digestText) {
			return fmt.Errorf("checksums.txt line %d is not canonical", index+1)
		}
		digest, err := hex.DecodeString(digestText)
		if err != nil || len(digest) != sha256.Size {
			return fmt.Errorf("checksums.txt line %d has invalid SHA-256", index+1)
		}
		actual := sha256.Sum256(artifacts[name])
		if !bytes.Equal(digest, actual[:]) {
			return fmt.Errorf("go module artifact %q has a SHA-256 mismatch", name)
		}
	}
	return nil
}

func artifactFactFor(name string, content []byte) artifactFact {
	digest := sha256.Sum256(content)
	return artifactFact{Name: name, SHA256: hex.EncodeToString(digest[:]), Size: len(content)}
}

func marshalCanonical(value any) ([]byte, error) {
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(content, '\n'), nil
}

func validateCanonicalJSON(actual, expected []byte, label string) error {
	if err := rejectDuplicateJSONKeys(actual); err != nil {
		return fmt.Errorf("validate %s: %w", label, err)
	}
	if !bytes.Equal(actual, expected) {
		return fmt.Errorf("%s does not exactly match the trusted source and renderer contract", label)
	}
	return nil
}

func revalidateArtifactSet(directory string, initial map[string][]byte, files []string) error {
	current, _, err := readArtifactSet(directory, files)
	if err != nil {
		return err
	}
	return compareArtifactMaps(current, initial, files)
}

func safeName(name string) bool {
	return name != "" && name != "." && name != ".." && filepath.Base(name) == name &&
		!strings.ContainsAny(name, `/\:`)
}
