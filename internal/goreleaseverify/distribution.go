package goreleaseverify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const distributionRendererIdentity = "github.com/spice-framework/development/cmd/spice-dev distribution-release renderer/v1"

// VerifyDistribution independently authenticates and rebuilds a closed-policy
// Go binary distribution. It never imports or executes the development
// renderer, and only verifier-owned bytes may leave this boundary.
func VerifyDistribution(ctx context.Context, config Config) (Result, error) {
	if ctx == nil {
		return Result{}, errors.New("verify Go distribution release: context is nil")
	}
	runner, err := newSystemGoRunner()
	if err != nil {
		return Result{}, err
	}
	return verifyDistribution(ctx, config, runner)
}

func verifyDistribution(
	ctx context.Context,
	config Config,
	runner distributionGoRunner,
) (_ Result, resultErr error) {
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("verify Go distribution release: %w", err)
	}
	policy, err := selectDistributionPolicy(config)
	if err != nil {
		return Result{}, err
	}
	return verifyDistributionPolicy(ctx, config, policy, runner)
}

func verifyDistributionPolicy(
	ctx context.Context,
	config Config,
	policy distributionPolicy,
	runner distributionGoRunner,
) (_ Result, resultErr error) {
	modulePolicy := distributionModulePolicy(policy)
	source, err := trustedSource(ctx, config, modulePolicy)
	if err != nil {
		return Result{}, err
	}
	modules, err := validateCommittedModuleForProfile(ctx, source, modulePolicy, ProfileDistribution)
	if err != nil {
		return Result{}, err
	}
	if payloadErr := requireDistributionPayloads(ctx, source, policy); payloadErr != nil {
		return Result{}, payloadErr
	}
	files := expectedDistributionAssetNames(policy)
	artifacts, directory, err := readDistributionArtifactSet(config.Directory, files)
	if err != nil {
		return Result{}, err
	}
	checksummed := slices.Clone(files)
	checksummed = slices.DeleteFunc(checksummed, func(name string) bool { return name == "checksums.txt" })
	if checksumErr := verifyChecksums(artifacts, checksummed); checksumErr != nil {
		return Result{}, checksumErr
	}

	archive, err := expectedSourceArchive(ctx, source, policy.repository, policy.version)
	if err != nil {
		return Result{}, err
	}
	workspace, err := materializeSourceArchive(archive, source, modulePolicy)
	if err != nil {
		return Result{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, workspace.Close()) }()
	if authenticationErr := authenticateVendorAndBuild(ctx, workspace, source, runner); authenticationErr != nil {
		return Result{}, authenticationErr
	}
	expected, err := rebuildDistribution(ctx, workspace, source, policy, modules, runner)
	if err != nil {
		return Result{}, err
	}
	if err := compareArtifactMaps(artifacts, expected, files); err != nil {
		return Result{}, err
	}
	if err := revalidateSource(ctx, source, policy.version); err != nil {
		return Result{}, err
	}
	if err := revalidateDistributionArtifactSet(directory, artifacts, files); err != nil {
		return Result{}, err
	}
	if _, err := writeDistributionVerifiedOutput(config.VerifiedOutput, artifacts, files); err != nil {
		return Result{}, err
	}
	return Result{
		Files: slices.Clone(files), Commit: source.commit, Epoch: source.epoch,
		Module: policy.module, Profile: ProfileDistribution,
	}, nil
}

func selectDistributionPolicy(config Config) (distributionPolicy, error) {
	if config.Profile != ProfileDistribution {
		return distributionPolicy{}, fmt.Errorf(
			"release profile %q is unsupported; require %s", config.Profile, ProfileDistribution,
		)
	}
	policy, found := distributionPolicies[config.RepositoryName]
	if !found {
		return distributionPolicy{}, fmt.Errorf(
			"repository %q is not independently authorized for %s",
			config.RepositoryName,
			ProfileDistribution,
		)
	}
	if config.Module != policy.module || config.CanonicalSource != policy.source ||
		config.Version != policy.version {
		return distributionPolicy{}, errors.New("trusted release inputs do not match independent distribution policy")
	}
	return policy, nil
}

func distributionModulePolicy(policy distributionPolicy) releasePolicy {
	return releasePolicy{
		repository: policy.repository, module: policy.module, source: policy.source,
		version: policy.version, metadataFile: policy.metadataFile,
		requiredModules: slices.Clone(policy.requiredModules),
	}
}

func requireDistributionPayloads(ctx context.Context, source sourceIdentity, policy distributionPolicy) error {
	for _, name := range policy.payloadFiles {
		if _, err := readGitBlob(ctx, source, name, maxDistributionArtifact); err != nil {
			return fmt.Errorf("validate committed distribution payload %q: %w", name, err)
		}
	}
	return nil
}

func expectedDistributionAssetNames(policy distributionPolicy) []string {
	base := policy.repository + "_" + strings.TrimPrefix(policy.version, "v")
	result := []string{
		"checksums.txt", base + "_release.json", base + "_sbom.spdx.json",
	}
	for _, target := range policy.targets {
		name := distributionTargetBase(policy, target)
		if target.goos == "windows" {
			name += ".zip"
		} else {
			name += ".tar.gz"
		}
		result = append(result, name)
	}
	slices.Sort(result)
	return result
}

func readDistributionArtifactSet(configured string, expected []string) (map[string][]byte, string, error) {
	directory, err := realDirectory(configured, "distribution artifact directory")
	if err != nil {
		return nil, "", err
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil, "", fmt.Errorf("read distribution artifact directory: %w", err)
	}
	actual := make([]string, 0, len(entries))
	for _, entry := range entries {
		info, infoErr := entry.Info()
		if infoErr != nil {
			return nil, "", fmt.Errorf("inspect distribution artifact %q: %w", entry.Name(), infoErr)
		}
		if !info.Mode().IsRegular() || info.Size() < 0 || info.Size() > maxDistributionArtifact {
			return nil, "", fmt.Errorf("distribution artifact %q is not a bounded regular file", entry.Name())
		}
		actual = append(actual, entry.Name())
	}
	slices.Sort(actual)
	if !slices.Equal(actual, expected) {
		return nil, "", fmt.Errorf("distribution artifact set is %v, require exactly %v", actual, expected)
	}
	result := make(map[string][]byte, len(expected))
	for _, name := range expected {
		maximum := int64(maxDistributionArtifact)
		switch {
		case name == "checksums.txt":
			maximum = maxChecksums
		case strings.HasSuffix(name, "_release.json"), strings.HasSuffix(name, "_sbom.spdx.json"):
			maximum = maxControlFile
		}
		content, readErr := readBoundedRegularFile(directory, name, maximum)
		if readErr != nil {
			return nil, "", readErr
		}
		result[name] = content
	}
	return result, directory, nil
}

func revalidateDistributionArtifactSet(directory string, initial map[string][]byte, files []string) error {
	current, _, err := readDistributionArtifactSet(directory, files)
	if err != nil {
		return err
	}
	return compareArtifactMaps(current, initial, files)
}

func rebuildDistribution(
	ctx context.Context,
	workspace isolatedWorkspace,
	source sourceIdentity,
	policy distributionPolicy,
	modules []selectedModule,
	runner distributionGoRunner,
) (map[string][]byte, error) {
	payloads := make(map[string][]byte, len(policy.payloadFiles))
	for _, name := range policy.payloadFiles {
		content, err := readWorkspaceFile(workspace.source, name, maxDistributionArtifact)
		if err != nil {
			return nil, fmt.Errorf("read isolated distribution payload %q: %w", name, err)
		}
		payloads[name] = content
	}
	result := make(map[string][]byte, len(policy.targets)+3)
	targetFacts := make([]distributionTargetFact, 0, len(policy.targets))
	for _, target := range policy.targets {
		binaries, err := buildDistributionTarget(ctx, workspace, source, policy, target, runner)
		if err != nil {
			return nil, err
		}
		archive, err := buildDistributionArchive(policy, source.epoch, target, binaries, payloads)
		if err != nil {
			return nil, err
		}
		name := distributionTargetBase(policy, target)
		if target.goos == "windows" {
			name += ".zip"
		} else {
			name += ".tar.gz"
		}
		result[name] = archive
		binaryNames := make([]string, 0, len(binaries))
		for binaryName := range binaries {
			binaryNames = append(binaryNames, binaryName)
		}
		slices.Sort(binaryNames)
		targetFacts = append(targetFacts, distributionTargetFact{
			GOOS: target.goos, GOARCH: target.goarch, Archive: name, Binaries: binaryNames,
		})
	}
	base := policy.repository + "_" + strings.TrimPrefix(policy.version, "v")
	sbomName := base + "_sbom.spdx.json"
	sbom, err := marshalCanonical(distributionSBOM(policy, source, modules))
	if err != nil {
		return nil, err
	}
	result[sbomName] = sbom
	metadataName := base + "_release.json"
	metadata, err := marshalCanonical(distributionMetadata(policy, source, targetFacts, payloads, result))
	if err != nil {
		return nil, err
	}
	result[metadataName] = metadata
	result["checksums.txt"] = checksumsForArtifacts(result)
	return result, nil
}

func distributionTargetBase(policy distributionPolicy, target distributionTarget) string {
	return policy.repository + "_" + strings.TrimPrefix(policy.version, "v") + "_" + target.goos + "_" + target.goarch
}

func checksumsForArtifacts(artifacts map[string][]byte) []byte {
	names := make([]string, 0, len(artifacts))
	for name := range artifacts {
		names = append(names, name)
	}
	slices.Sort(names)
	var output strings.Builder
	for _, name := range names {
		digest := sha256.Sum256(artifacts[name])
		fmt.Fprintf(&output, "%s  %s\n", hex.EncodeToString(digest[:]), name)
	}
	return []byte(output.String())
}

func writeDistributionVerifiedOutput(
	configured string,
	artifacts map[string][]byte,
	files []string,
) (_ string, resultErr error) {
	if configured == "" {
		return "", errors.New("verified output directory is required")
	}
	target, err := filepath.Abs(configured)
	if err != nil {
		return "", fmt.Errorf("resolve verified output directory: %w", err)
	}
	name := filepath.Base(target)
	if !safeName(name) {
		return "", fmt.Errorf("verified output directory name %q is unsafe", name)
	}
	parent, err := realDirectory(filepath.Dir(target), "verified output parent")
	if err != nil {
		return "", err
	}
	target = filepath.Join(parent, name)
	if _, statErr := os.Lstat(target); !errors.Is(statErr, os.ErrNotExist) {
		if statErr == nil {
			return "", fmt.Errorf("verified output directory %q already exists", target)
		}
		return "", fmt.Errorf("inspect verified output directory: %w", statErr)
	}
	if mkdirErr := os.Mkdir(target, 0o700); mkdirErr != nil {
		return "", fmt.Errorf("claim absent verified output directory without replacement: %w", mkdirErr)
	}
	owned := true
	defer func() {
		if owned {
			resultErr = errors.Join(resultErr, os.RemoveAll(target))
		}
	}()
	root, err := os.OpenRoot(target)
	if err != nil {
		return "", fmt.Errorf("open claimed verified output root: %w", err)
	}
	for _, fileName := range files {
		if err := writeVerifiedFile(root, fileName, artifacts[fileName]); err != nil {
			return "", errors.Join(err, root.Close())
		}
	}
	if err := root.Close(); err != nil {
		return "", err
	}
	if err := revalidateDistributionArtifactSet(target, artifacts, files); err != nil {
		return "", fmt.Errorf("recheck verifier-owned output: %w", err)
	}
	owned = false
	return target, nil
}

type distributionReleaseMetadata struct {
	Schema          int                      `json:"schema"`
	Profile         string                   `json:"profile"`
	Repository      string                   `json:"repository"`
	Module          string                   `json:"module"`
	Source          string                   `json:"source"`
	Version         string                   `json:"version"`
	Commit          string                   `json:"commit"`
	SourceDateEpoch int64                    `json:"source_date_epoch"`
	Go              string                   `json:"go"`
	Toolchain       string                   `json:"toolchain"`
	Build           distributionBuildFact    `json:"build"`
	Targets         []distributionTargetFact `json:"targets"`
	Payloads        []artifactFact           `json:"payloads"`
	Artifacts       []artifactFact           `json:"artifacts"`
}

type distributionBuildFact struct {
	ModuleMode     string                        `json:"module_mode"`
	CGOEnabled     bool                          `json:"cgo_enabled"`
	Trimpath       bool                          `json:"trimpath"`
	BuildVCS       bool                          `json:"build_vcs"`
	BuildID        string                        `json:"build_id"`
	Environment    string                        `json:"environment"`
	CacheIsolation bool                          `json:"cache_isolation"`
	Source         string                        `json:"source"`
	GOAMD64        string                        `json:"goamd64"`
	GOARM64        string                        `json:"goarm64"`
	Identity       distributionBuildIdentityFact `json:"identity"`
}

type distributionBuildIdentityFact struct {
	VersionSymbol string `json:"version_symbol"`
	VersionValue  string `json:"version_value"`
	CommitSymbol  string `json:"commit_symbol"`
	CommitValue   string `json:"commit_value"`
}

type distributionTargetFact struct {
	GOOS     string   `json:"goos"`
	GOARCH   string   `json:"goarch"`
	Archive  string   `json:"archive"`
	Binaries []string `json:"binaries"`
}

func distributionMetadata(
	policy distributionPolicy,
	source sourceIdentity,
	targets []distributionTargetFact,
	payloads map[string][]byte,
	artifacts map[string][]byte,
) distributionReleaseMetadata {
	return distributionReleaseMetadata{
		Schema: artifactSchema, Profile: ProfileDistribution,
		Repository: policy.repository, Module: policy.module, Source: policy.source,
		Version: policy.version, Commit: source.commit, SourceDateEpoch: source.epoch.Unix(),
		Go: "1.26.5", Toolchain: "go1.26.5",
		Build: distributionBuildFact{
			ModuleMode: "vendor", CGOEnabled: false, Trimpath: true, BuildVCS: false,
			BuildID: "", Environment: "closed", CacheIsolation: true,
			Source: "materialized-tagged-commit", GOAMD64: "v1", GOARM64: "v8.0",
			Identity: distributionBuildIdentityFact{
				VersionSymbol: policy.versionSymbol, VersionValue: strings.TrimPrefix(policy.version, "v"),
				CommitSymbol: policy.commitSymbol, CommitValue: source.commit,
			},
		},
		Targets: targets, Payloads: distributionFacts(payloads), Artifacts: distributionFacts(artifacts),
	}
}

func distributionFacts(values map[string][]byte) []artifactFact {
	result := make([]artifactFact, 0, len(values))
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		result = append(result, artifactFactFor(name, values[name]))
	}
	return result
}

func distributionSBOM(
	policy distributionPolicy,
	source sourceIdentity,
	modules []selectedModule,
) spdxDocument {
	rootID := packageID(policy.module, policy.version)
	packages := []spdxPackage{spdxPackageValue(policy.module, policy.version)}
	relationships := []spdxRelationship{{
		SPDXElementID: "SPDXRef-DOCUMENT", RelationshipType: "DESCRIBES", RelatedSPDXElement: rootID,
	}}
	for _, item := range modules {
		packages = append(packages, spdxPackageValue(item.path, item.version))
		relationships = append(relationships, spdxRelationship{
			SPDXElementID: rootID, RelationshipType: "DEPENDS_ON",
			RelatedSPDXElement: packageID(item.path, item.version),
		})
	}
	digest := sha256.Sum256([]byte(policy.module + "@" + policy.version + "@" + source.commit))
	return spdxDocument{
		SPDXVersion: "SPDX-2.3", DataLicense: "CC0-1.0", SPDXID: "SPDXRef-DOCUMENT",
		Name: policy.repository + " " + policy.version,
		DocumentNamespace: strings.TrimSuffix(policy.source, "/") + "/releases/" + policy.version +
			"/spdx/distribution-v1/" + hex.EncodeToString(digest[:]),
		CreationInfo: spdxCreationInfo{
			Created:  source.epoch.UTC().Format(time.RFC3339),
			Creators: []string{"Organization: Spice Framework", "Tool: " + distributionRendererIdentity},
		},
		Packages: packages, Relationships: relationships,
	}
}
