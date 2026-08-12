package goreleaseverify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestToolchainPreviewSevenDistributionPolicyIsClosed(t *testing.T) {
	t.Parallel()
	policy := distributionPolicies["toolchain"]
	valid := Config{
		RepositoryName: policy.repository, CanonicalSource: policy.source,
		Module: policy.module, Version: policy.version, Profile: ProfileDistribution,
	}
	if _, err := selectDistributionPolicy(valid); err != nil {
		t.Fatalf("selectDistributionPolicy(Toolchain) error = %v", err)
	}
	wantModules := []selectedModule{{
		path: "github.com/spice-framework/spice", version: "v0.1.0-preview.4",
	}}
	wantBinaries := []distributionBinary{{name: "spice", packagePath: "./cmd/spice"}}
	wantTargets := []distributionTarget{
		{goos: "linux", goarch: "amd64"},
		{goos: "linux", goarch: "arm64"},
		{goos: "darwin", goarch: "amd64"},
		{goos: "darwin", goarch: "arm64"},
		{goos: "windows", goarch: "amd64"},
		{goos: "windows", goarch: "arm64"},
	}
	if policy.repository != "toolchain" || policy.module != "github.com/spice-framework/toolchain" ||
		policy.source != "https://github.com/spice-framework/toolchain" ||
		policy.version != "v0.1.0-preview.7" || policy.metadataFile != "spice-release.json" ||
		!slices.Equal(policy.requiredModules, wantModules) || !slices.Equal(policy.binaries, wantBinaries) ||
		!slices.Equal(policy.targets, wantTargets) || !slices.Equal(policy.payloadFiles, []string{"LICENSE", "README.md"}) ||
		policy.versionSymbol != "github.com/spice-framework/toolchain/internal/cli.Version" ||
		policy.commitSymbol != "github.com/spice-framework/toolchain/internal/cli.Commit" {
		t.Fatalf("Toolchain distribution policy = %#v", policy)
	}
	if got := len(expectedDistributionAssetNames(policy)); got != 9 {
		t.Fatalf("Toolchain distribution artifact subjects = %d, want 9", got)
	}
	for _, version := range []string{
		"v0.1.0-preview.1",
		"v0.1.0-preview.2",
		"v0.1.0-preview.3",
		"v0.1.0-preview.4",
		"v0.1.0-preview.5",
		"v0.1.0-preview.6",
	} {
		stale := valid
		stale.Version = version
		if _, err := selectDistributionPolicy(stale); err == nil {
			t.Fatalf("selectDistributionPolicy(Toolchain %s) error = nil", version)
		}
	}
}

func TestDistributionPolicyIsClosed(t *testing.T) {
	t.Parallel()
	policy := distributionPolicies["spice-agent-coding"]
	valid := Config{
		RepositoryName: policy.repository, CanonicalSource: policy.source,
		Module: policy.module, Version: policy.version, Profile: ProfileDistribution,
	}
	if _, err := selectDistributionPolicy(valid); err != nil {
		t.Fatalf("selectDistributionPolicy(valid) error = %v", err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*Config)
	}{
		{name: "profile", mutate: func(value *Config) { value.Profile = ProfileGoModule }},
		{name: "repository", mutate: func(value *Config) { value.RepositoryName = "spice-agent" }},
		{name: "module", mutate: func(value *Config) { value.Module = "example.com/other" }},
		{name: "source", mutate: func(value *Config) { value.CanonicalSource += "/fork" }},
		{name: "stale preview.1 version", mutate: func(value *Config) { value.Version = "v0.1.0-preview.1" }},
		{name: "stale preview.2 version", mutate: func(value *Config) { value.Version = "v0.1.0-preview.2" }},
		{name: "stale preview.3 version", mutate: func(value *Config) { value.Version = "v0.1.0-preview.3" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			candidate := valid
			test.mutate(&candidate)
			if _, err := selectDistributionPolicy(candidate); err == nil {
				t.Fatal("selectDistributionPolicy(invalid) error = nil")
			}
		})
	}
	if len(expectedDistributionAssetNames(policy)) != len(policy.targets)+3 {
		t.Fatal("distribution asset allowlist does not bind every target")
	}
	wantModules := []selectedModule{
		{path: "github.com/spice-framework/spice", version: historicalSpiceFoundationVersion},
		{path: "github.com/spice-framework/toolchain", version: historicalCodingToolchainVersion},
		{path: "github.com/spice-framework/spice-agent", version: agentCoreDependencyVersion},
		{path: "github.com/spice-framework/spice-agent-provider-openai", version: historicalCodingProviderVersion},
		{path: "github.com/spice-framework/spice-agent-tools-coding", version: historicalCodingToolsVersion},
		{path: "github.com/spice-framework/spice-agent-tui", version: historicalCodingTUIVersion},
	}
	if !slices.Equal(policy.requiredModules, wantModules) {
		t.Fatalf("distribution required modules = %#v, want %#v", policy.requiredModules, wantModules)
	}
	if policy.version != agentDistributionVersion {
		t.Fatalf("distribution version = %q, want %q", policy.version, agentDistributionVersion)
	}
}

func TestDistributionArchiveAndMetadataAreDeterministic(t *testing.T) {
	t.Parallel()
	policy := distributionPolicies["spice-agent-coding"]
	epoch := time.Date(2026, time.August, 7, 12, 0, 0, 0, time.UTC)
	for _, target := range []distributionTarget{{goos: "linux", goarch: "amd64"}, {goos: "windows", goarch: "amd64"}} {
		binaries := map[string][]byte{"spice-agent": []byte("agent"), "spice-agentd": []byte("daemon")}
		if target.goos == "windows" {
			binaries = map[string][]byte{"spice-agent.exe": []byte("agent"), "spice-agentd.exe": []byte("daemon")}
		}
		payloads := map[string][]byte{"README.md": []byte("readme")}
		left, err := buildDistributionArchive(policy, epoch, target, binaries, payloads)
		if err != nil {
			t.Fatalf("buildDistributionArchive(%s) error = %v", target.goos, err)
		}
		right, err := buildDistributionArchive(policy, epoch, target, binaries, payloads)
		if err != nil || !bytes.Equal(left, right) {
			t.Fatalf("buildDistributionArchive(%s) is nondeterministic: %v", target.goos, err)
		}
	}

	source := sourceIdentity{commit: strings.Repeat("a", 40), epoch: epoch}
	metadata := distributionMetadata(
		policy,
		source,
		[]distributionTargetFact{{GOOS: "linux", GOARCH: "amd64", Archive: "archive", Binaries: []string{"tool"}}},
		map[string][]byte{"README.md": []byte("readme")},
		map[string][]byte{"archive": []byte("archive")},
	)
	if metadata.Profile != ProfileDistribution || metadata.Build.Identity.CommitValue != source.commit ||
		metadata.Build.Environment != "closed" || len(metadata.Payloads) != 1 || len(metadata.Artifacts) != 1 {
		t.Fatalf("distributionMetadata() = %#v", metadata)
	}
	sbom := distributionSBOM(policy, source, []selectedModule{{path: "example.com/dependency", version: "v1.2.3"}})
	if !strings.Contains(sbom.DocumentNamespace, "/spdx/distribution-v1/") || len(sbom.Packages) != 2 {
		t.Fatalf("distributionSBOM() = %#v", sbom)
	}
}

func TestVerifyDistributionEndToEndWithIndependentRunner(t *testing.T) {
	fixture := newDistributionVerifierFixture(t)
	result, err := verifyDistributionPolicy(t.Context(), fixture.config, fixture.runner.policy, fixture.runner)
	if err != nil {
		t.Fatalf("verifyDistribution() error = %v", err)
	}
	if result.Profile != ProfileDistribution || result.Commit != fixture.config.Commit ||
		!slices.Equal(result.Files, fixture.files) {
		t.Fatalf("verifyDistribution() = %#v", result)
	}
	for _, name := range fixture.files {
		want, readErr := os.ReadFile(filepath.Join(fixture.artifacts, name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		got, readErr := os.ReadFile(filepath.Join(fixture.config.VerifiedOutput, name))
		if readErr != nil || !bytes.Equal(got, want) {
			t.Fatalf("verifier-owned %s mismatch: %v", name, readErr)
		}
	}
}

func TestVerifyDistributionRejectsArtifactMutation(t *testing.T) {
	fixture := newDistributionVerifierFixture(t)
	archive := ""
	for _, name := range fixture.files {
		if strings.HasSuffix(name, ".tar.gz") {
			archive = name
			break
		}
	}
	writeFile(t, filepath.Join(fixture.artifacts, archive), []byte("tampered"))
	rewriteDistributionChecksums(t, fixture)
	if _, err := verifyDistributionPolicy(t.Context(), fixture.config, fixture.runner.policy, fixture.runner); err == nil ||
		!strings.Contains(err.Error(), "changed during verification") {
		t.Fatalf("verifyDistribution(tampered) error = %v", err)
	}
}

func TestDistributionArtifactAndEnvironmentBoundaries(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	writeFile(t, filepath.Join(directory, "artifact"), []byte("data"))
	artifacts, resolved, err := readDistributionArtifactSet(directory, []string{"artifact"})
	if err != nil || resolved == "" || string(artifacts["artifact"]) != "data" {
		t.Fatalf("readDistributionArtifactSet() = %#v, %q, %v", artifacts, resolved, err)
	}
	writeFile(t, filepath.Join(directory, "extra"), []byte("data"))
	if _, _, err := readDistributionArtifactSet(directory, []string{"artifact"}); err == nil {
		t.Fatal("readDistributionArtifactSet(extra) error = nil")
	}

	workspace := isolatedWorkspace{
		moduleCache: "module", buildCache: "build", goPath: "path", temporary: "temporary",
	}
	environment := distributionBuildEnvironment(
		[]string{"GOFLAGS=ambient", "GOOS=ambient", "HTTP_PROXY=unsafe"},
		workspace,
		distributionTarget{goos: "linux", goarch: "amd64"},
	)
	values := environmentValues(environment)
	if values["GOOS"] != "linux" || values["GOARCH"] != "amd64" || values["GOAMD64"] != "v1" ||
		values["GOPROXY"] != "off" || values["GOSUMDB"] != "off" || values["GOFLAGS"] != "" {
		t.Fatalf("distributionBuildEnvironment() = %#v", values)
	}
	if _, found := values["HTTP_PROXY"]; found {
		t.Fatal("distribution build retained ambient proxy")
	}
}

func TestDistributionExecutionAndOutputBoundaries(t *testing.T) {
	t.Parallel()
	//nolint:staticcheck // The public contract explicitly rejects a nil context.
	if _, err := VerifyDistribution(nil, Config{}); err == nil || !strings.Contains(err.Error(), "context is nil") {
		t.Fatalf("VerifyDistribution(nil) error = %v", err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := VerifyDistribution(canceled, Config{}); err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("VerifyDistribution(canceled) error = %v", err)
	}
	runner, err := newSystemGoRunner()
	if err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err := runner.Execute(t.Context(), t.TempDir(), os.Environ(), runner.executable, "version")
	if err != nil || len(stderr) != 0 || !bytes.Contains(stdout, []byte("go version go1.26.5")) {
		t.Fatalf("system runner version = %q, %q, %v", stdout, stderr, err)
	}
	if _, _, executeErr := runner.Execute(canceled, t.TempDir(), os.Environ(), runner.executable, "version"); !errors.Is(executeErr, context.Canceled) {
		t.Fatalf("system runner canceled error = %v", executeErr)
	}

	parent := t.TempDir()
	artifacts := map[string][]byte{"artifact": []byte("verified")}
	for _, output := range []string{
		"",
		filepath.Join(parent, "bad:name"),
		filepath.Join(parent, "missing", "output"),
	} {
		if _, outputErr := writeDistributionVerifiedOutput(output, artifacts, []string{"artifact"}); outputErr == nil {
			t.Errorf("writeDistributionVerifiedOutput(%q) error = nil", output)
		}
	}
	existing := filepath.Join(parent, "existing")
	if mkdirErr := os.Mkdir(existing, 0o700); mkdirErr != nil {
		t.Fatal(mkdirErr)
	}
	if _, outputErr := writeDistributionVerifiedOutput(existing, artifacts, []string{"artifact"}); outputErr == nil {
		t.Fatal("writeDistributionVerifiedOutput(existing) error = nil")
	}

	file := filepath.Join(parent, "file")
	writeFile(t, file, []byte("data"))
	content, err := readAbsoluteRegularFile(file, 4)
	if err != nil || string(content) != "data" {
		t.Fatalf("readAbsoluteRegularFile(valid) = %q, %v", content, err)
	}
	if _, err := readAbsoluteRegularFile(file, 3); err == nil {
		t.Fatal("readAbsoluteRegularFile(oversized) error = nil")
	}
	if _, err := readAbsoluteRegularFile(parent, maxControlFile); err == nil {
		t.Fatal("readAbsoluteRegularFile(directory) error = nil")
	}

	buffer := distributionArchiveBuffer{maximum: 3}
	if written, err := buffer.Write([]byte("ab")); err != nil || written != 2 {
		t.Fatalf("archive buffer first write = %d, %v", written, err)
	}
	if written, err := buffer.Write([]byte("cd")); err == nil || written != 1 {
		t.Fatalf("archive buffer bounded write = %d, %v", written, err)
	}
	if written, err := buffer.Write([]byte("e")); err == nil || written != 0 {
		t.Fatalf("archive buffer full write = %d, %v", written, err)
	}
}

type distributionVerifierFixture struct {
	root      string
	artifacts string
	config    Config
	files     []string
	runner    *distributionFixtureRunner
}

func newDistributionVerifierFixture(t *testing.T) distributionVerifierFixture {
	t.Helper()
	parent := t.TempDir()
	root := filepath.Join(parent, "repository")
	artifacts := filepath.Join(parent, "artifacts")
	for _, directory := range []string{root, filepath.Join(root, "vendor"), artifacts} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	policy := distributionPolicy{
		repository: "distribution-fixture", module: "example.com/distribution-fixture",
		source: "https://github.com/spice-framework/distribution-fixture", version: distributionFixtureVersion,
		metadataFile:    "spice-release.json",
		requiredModules: []selectedModule{{path: "example.com/dependency", version: "v1.0.0"}},
		binaries:        []distributionBinary{{name: "fixture", packagePath: "./cmd/fixture"}},
		targets: []distributionTarget{
			{goos: runtime.GOOS, goarch: runtime.GOARCH},
			{goos: alternateDistributionOS(), goarch: runtime.GOARCH},
		},
		payloadFiles:  []string{"README.md"},
		versionSymbol: "example.com/distribution-fixture/internal/distribution.Version",
		commitSymbol:  "example.com/distribution-fixture/internal/distribution.Commit",
	}
	writeFile(t, filepath.Join(root, "go.mod"), []byte(
		"module "+policy.module+"\n\ngo 1.26.0\n\ntoolchain go1.26.5\n\nrequire example.com/dependency v1.0.0\n",
	))
	writeFile(t, filepath.Join(root, "go.sum"), []byte("example.com/dependency v1.0.0 h1:fixture=\n"))
	vendorMetadata := []byte("# example.com/dependency v1.0.0\n## explicit; go 1.26.0\n")
	writeFile(t, filepath.Join(root, "vendor", "modules.txt"), vendorMetadata)
	writeFile(t, filepath.Join(root, "LICENSE"), []byte("Apache-2.0\n"))
	writeFile(t, filepath.Join(root, "README.md"), []byte("# distribution fixture\n"))
	if err := os.MkdirAll(filepath.Join(root, "cmd", "fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "cmd", "fixture", "main.go"), []byte("package main\nfunc main() {}\n"))
	writeFile(t, filepath.Join(root, "spice-release.json"), []byte(fmt.Sprintf(`{
  "schema": 1,
  "profile": "go-distribution-v1",
  "repository": %q,
  "module": %q,
  "version": %q
}
`, policy.repository, policy.module, policy.version)))
	runGit(t, root, "init", "-q")
	runGit(t, root, "config", "user.name", "Spice Test")
	runGit(t, root, "config", "user.email", "test@spice.invalid")
	runGit(t, root, "remote", "add", "origin", policy.source+".git")
	runGit(t, root, "add", ".")
	runGitEnv(t, root, []string{
		"GIT_AUTHOR_DATE=2026-08-07T00:00:00Z", "GIT_COMMITTER_DATE=2026-08-07T00:00:00Z",
	}, "commit", "-q", "-m", "fixture")
	runGit(t, root, "tag", policy.version)
	commit := strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD"))
	epoch := time.Date(2026, time.August, 7, 0, 0, 0, 0, time.UTC)
	source := sourceIdentity{commit: commit, epoch: epoch}
	result := make(map[string][]byte)
	targets := make([]distributionTargetFact, 0, len(policy.targets))
	for _, target := range policy.targets {
		binaryName := "fixture"
		if target.goos == "windows" {
			binaryName += ".exe"
		}
		binaries := map[string][]byte{binaryName: distributionFixtureBinary(binaryName, target)}
		archive, err := buildDistributionArchive(
			policy, epoch, target, binaries, map[string][]byte{"README.md": []byte("# distribution fixture\n")},
		)
		if err != nil {
			t.Fatal(err)
		}
		name := distributionTargetBase(policy, target)
		if target.goos == "windows" {
			name += ".zip"
		} else {
			name += ".tar.gz"
		}
		result[name] = archive
		targets = append(targets, distributionTargetFact{
			GOOS: target.goos, GOARCH: target.goarch, Archive: name, Binaries: []string{binaryName},
		})
	}
	base := policy.repository + "_" + strings.TrimPrefix(policy.version, "v")
	sbomName := base + "_sbom.spdx.json"
	sbom, err := marshalCanonical(distributionSBOM(policy, source, policy.requiredModules))
	if err != nil {
		t.Fatal(err)
	}
	result[sbomName] = sbom
	metadataName := base + "_release.json"
	metadata, err := marshalCanonical(distributionMetadata(
		policy, source, targets,
		map[string][]byte{"README.md": []byte("# distribution fixture\n")}, result,
	))
	if err != nil {
		t.Fatal(err)
	}
	result[metadataName] = metadata
	result["checksums.txt"] = checksumsForArtifacts(result)
	files := expectedDistributionAssetNames(policy)
	for _, name := range files {
		writeFile(t, filepath.Join(artifacts, name), result[name])
	}
	return distributionVerifierFixture{
		root: root, artifacts: artifacts, files: files,
		config: Config{
			Directory: artifacts, VerifiedOutput: filepath.Join(parent, "verified"),
			Repository: root, RepositoryName: policy.repository,
			CanonicalSource: policy.source, Module: policy.module, Version: policy.version,
			Commit: commit, Profile: ProfileDistribution,
		},
		runner: &distributionFixtureRunner{
			policy: policy, commit: commit,
			vendor: map[string][]byte{"modules.txt": vendorMetadata},
		},
	}
}

func alternateDistributionOS() string {
	if runtime.GOOS == "windows" {
		return "linux"
	}
	return "windows"
}

func distributionFixtureBinary(name string, target distributionTarget) []byte {
	return []byte("binary:" + name + ":" + target.goos + "/" + target.goarch + "\n")
}

func rewriteDistributionChecksums(t *testing.T, fixture distributionVerifierFixture) {
	t.Helper()
	artifacts := make(map[string][]byte, len(fixture.files)-1)
	for _, name := range fixture.files {
		if name == "checksums.txt" {
			continue
		}
		content, err := os.ReadFile(filepath.Join(fixture.artifacts, name))
		if err != nil {
			t.Fatal(err)
		}
		artifacts[name] = content
	}
	writeFile(t, filepath.Join(fixture.artifacts, "checksums.txt"), checksumsForArtifacts(artifacts))
}

type distributionFixtureRunner struct {
	policy distributionPolicy
	commit string
	vendor map[string][]byte
}

func (runner *distributionFixtureRunner) Output(
	_ context.Context,
	root string,
	environment []string,
	arguments ...string,
) ([]byte, error) {
	command := strings.Join(arguments, " ")
	switch command {
	case "version":
		return []byte("go version go1.26.5 fixture\n"), nil
	case "mod download -modfile=" + authenticationModFile + " all", "mod verify",
		"list -mod=vendor ./...", "build -mod=vendor -trimpath ./...":
		return nil, nil
	}
	if len(arguments) == 5 && arguments[0] == "mod" && arguments[1] == "vendor" &&
		arguments[2] == "-modfile="+authenticationModFile && arguments[3] == "-o" {
		if err := os.Mkdir(arguments[4], 0o700); err != nil {
			return nil, err
		}
		for name, content := range runner.vendor {
			if err := os.WriteFile(filepath.Join(arguments[4], name), content, 0o600); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}
	if len(arguments) > 0 && arguments[0] == "build" {
		output := argumentAfter(arguments, "-o")
		if output == "" {
			return nil, errors.New("fixture build has no output")
		}
		values := environmentValues(environment)
		target := distributionTarget{goos: values["GOOS"], goarch: values["GOARCH"]}
		name := filepath.Base(output)
		if err := os.WriteFile(output, distributionFixtureBinary(name, target), 0o700); err != nil {
			return nil, err
		}
		return nil, nil
	}
	if len(arguments) == 3 && arguments[0] == "tool" && arguments[1] == "nm" {
		return []byte("0 D " + runner.policy.versionSymbol + "\n1 D " + runner.policy.commitSymbol + "\n"), nil
	}
	return nil, fmt.Errorf("unexpected distribution fixture Go command %q in %s", command, root)
}

func (runner *distributionFixtureRunner) Execute(
	_ context.Context,
	_ string,
	_ []string,
	executable string,
	arguments ...string,
) ([]byte, []byte, error) {
	if !slices.Equal(arguments, []string{"--version"}) {
		return nil, nil, errors.New("unexpected fixture executable arguments")
	}
	name := strings.TrimSuffix(filepath.Base(executable), ".exe")
	stdout := name + " " + strings.TrimPrefix(runner.policy.version, "v") + " (" + runner.commit + ")\n"
	return []byte(stdout), nil, nil
}

func argumentAfter(arguments []string, name string) string {
	for index := range len(arguments) - 1 {
		if arguments[index] == name {
			return arguments[index+1]
		}
	}
	return ""
}

func environmentValues(environment []string) map[string]string {
	result := make(map[string]string, len(environment))
	for _, entry := range environment {
		name, value, found := strings.Cut(entry, "=")
		if found {
			result[strings.ToUpper(name)] = value
		}
	}
	return result
}
