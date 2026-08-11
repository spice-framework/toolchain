// Package goreleaseverify independently verifies catalog-authorized generic
// Go module release artifacts. It deliberately does not import the development
// repository or any release renderer.
package goreleaseverify

import "time"

const (
	ProfileGoModule     = "go-module-v1"
	ProfileDistribution = "go-distribution-v1"

	metadataSchema = 1
	artifactSchema = 1

	maxArtifactBytes        = 256 << 20
	maxArchiveBytes         = 256 << 20
	maxChecksums            = 64 << 10
	maxControlFile          = 1 << 20
	maxModuleGraph          = 16 << 20
	maxGitTree              = 16 << 20
	maxDiagnostic           = 32 << 10
	maxArchiveSource        = 256 << 20
	maxDistributionArtifact = 512 << 20
	maxTreeEntries          = 100_000
	maxVendorFiles          = 100_000
	maxVendorBytes          = 512 << 20

	rendererIdentity                 = "github.com/spice-framework/development/cmd/spice-dev go-release renderer/v1"
	historicalSpiceFoundationVersion = "v0.1.0-preview.2"
	historicalModuleToolchainVersion = "v0.1.0-preview.1.0.20260806203056-d0b9ac086bd6"
	historicalCodingToolchainVersion = "v0.1.0-preview.1.0.20260807044408-6598abca8196"
	spiceFoundationVersion           = "v0.1.0-preview.4"
	toolchainDistributionVersion     = "v0.1.0-preview.4"
	agentCoreReleaseVersion          = "v0.1.0-preview.7"
	agentCoreSpiceVersion            = "v0.1.0-preview.4"
	agentCoreToolchainVersion        = "v0.1.0-preview.2"
	agentCoreDependencyVersion       = "v0.1.0-preview.4"
	agentProviderReleaseVersion      = "v0.1.0-preview.1"
	agentCodingToolsReleaseVersion   = "v0.1.0-preview.1"
	historicalCodingProviderVersion  = "v0.1.0-preview.1"
	historicalCodingToolsVersion     = "v0.1.0-preview.1"
	historicalCodingTUIVersion       = "v0.1.0-preview.1"
	agentTUIReleaseVersion           = "v0.1.0-preview.2"
	agentDistributionVersion         = "v0.1.0-preview.4"
)

// Config contains separately trusted release identity and untrusted artifact
// locations. Policy is selected only from the verifier's compiled allowlist.
type Config struct {
	Directory       string
	Repository      string
	RepositoryName  string
	CanonicalSource string
	Module          string
	Version         string
	Commit          string
	Profile         string
	VerifiedOutput  string
}

// PolicyRequest is the artifact-free identity checked before an immutable tag
// is created. It never selects policy from ambient repository state.
type PolicyRequest struct {
	Repository string
	Source     string
	Module     string
	Version    string
	Profile    string
}

// PolicyAuthorization is the exact closed-policy identity authorized by the
// independent verifier.
type PolicyAuthorization struct {
	Repository string
	Source     string
	Module     string
	Version    string
	Profile    string
}

// Result summarizes a successfully verified generic Go module release.
type Result struct {
	Files   []string
	Commit  string
	Epoch   time.Time
	Module  string
	Profile string
}

type releasePolicy struct {
	repository      string
	module          string
	source          string
	version         string
	metadataFile    string
	requiredModules []selectedModule
}

type distributionPolicy struct {
	repository      string
	module          string
	source          string
	version         string
	metadataFile    string
	requiredModules []selectedModule
	binaries        []distributionBinary
	targets         []distributionTarget
	payloadFiles    []string
	versionSymbol   string
	commitSymbol    string
}

type distributionBinary struct {
	name        string
	packagePath string
}

type distributionTarget struct {
	goos   string
	goarch string
}

// The distribution policy deliberately duplicates the development catalog.
// The organization workflow pins the renderer and verifier independently, so
// neither repository can expand release authority by itself.
var distributionPolicies = map[string]distributionPolicy{
	"toolchain": {
		repository:   "toolchain",
		module:       "github.com/spice-framework/toolchain",
		source:       "https://github.com/spice-framework/toolchain",
		version:      toolchainDistributionVersion,
		metadataFile: "spice-release.json",
		requiredModules: []selectedModule{
			{path: "github.com/spice-framework/spice", version: spiceFoundationVersion},
		},
		binaries: []distributionBinary{
			{name: "spice", packagePath: "./cmd/spice"},
		},
		targets: []distributionTarget{
			{goos: "linux", goarch: "amd64"},
			{goos: "linux", goarch: "arm64"},
			{goos: "darwin", goarch: "amd64"},
			{goos: "darwin", goarch: "arm64"},
			{goos: "windows", goarch: "amd64"},
			{goos: "windows", goarch: "arm64"},
		},
		payloadFiles:  []string{"LICENSE", "README.md"},
		versionSymbol: "github.com/spice-framework/toolchain/internal/cli.Version",
		commitSymbol:  "github.com/spice-framework/toolchain/internal/cli.Commit",
	},
	"spice-agent-coding": {
		repository:   "spice-agent-coding",
		module:       "github.com/spice-framework/spice-agent-coding",
		source:       "https://github.com/spice-framework/spice-agent-coding",
		version:      agentDistributionVersion,
		metadataFile: "spice-release.json",
		requiredModules: []selectedModule{
			{path: "github.com/spice-framework/spice", version: historicalSpiceFoundationVersion},
			{path: "github.com/spice-framework/toolchain", version: historicalCodingToolchainVersion},
			{path: "github.com/spice-framework/spice-agent", version: agentCoreDependencyVersion},
			{path: "github.com/spice-framework/spice-agent-provider-openai", version: historicalCodingProviderVersion},
			{path: "github.com/spice-framework/spice-agent-tools-coding", version: historicalCodingToolsVersion},
			{path: "github.com/spice-framework/spice-agent-tui", version: historicalCodingTUIVersion},
		},
		binaries: []distributionBinary{
			{name: "spice-agent", packagePath: "./cmd/spice-agent"},
			{name: "spice-agentd", packagePath: "./cmd/spice-agentd"},
		},
		targets: []distributionTarget{
			{goos: "linux", goarch: "amd64"},
			{goos: "linux", goarch: "arm64"},
			{goos: "darwin", goarch: "amd64"},
			{goos: "darwin", goarch: "arm64"},
			{goos: "windows", goarch: "amd64"},
			{goos: "windows", goarch: "arm64"},
		},
		payloadFiles: []string{
			"LICENSE", "README.md", "THIRD_PARTY_NOTICES.md", "docs/configuration.md",
			"docs/installation.md", "docs/security.md", "protocol-descriptors.pb",
		},
		versionSymbol: "github.com/spice-framework/spice-agent-coding/internal/distribution.Version",
		commitSymbol:  "github.com/spice-framework/spice-agent-coding/internal/distribution.Commit",
	},
}

// These policies intentionally duplicate the development catalog. The
// organization release workflow pins both implementations so one repository
// cannot expand release authority on its own.
var releasePolicies = map[string]releasePolicy{
	"spice": {
		repository: "spice", module: "github.com/spice-framework/spice",
		source: "https://github.com/spice-framework/spice", version: spiceFoundationVersion,
		metadataFile: "spice-release.json",
	},
	"spice-agent": {
		repository: "spice-agent", module: "github.com/spice-framework/spice-agent",
		source: "https://github.com/spice-framework/spice-agent", version: agentCoreReleaseVersion,
		metadataFile: "spice-release.json",
		requiredModules: []selectedModule{
			{path: "github.com/spice-framework/spice", version: agentCoreSpiceVersion},
			{path: "github.com/spice-framework/toolchain", version: agentCoreToolchainVersion},
		},
	},
	"spice-agent-provider-openai": {
		repository: "spice-agent-provider-openai", module: "github.com/spice-framework/spice-agent-provider-openai",
		source: "https://github.com/spice-framework/spice-agent-provider-openai", version: agentProviderReleaseVersion,
		metadataFile: "spice-release.json",
		requiredModules: []selectedModule{
			{path: "github.com/spice-framework/spice", version: historicalSpiceFoundationVersion},
			{path: "github.com/spice-framework/toolchain", version: historicalModuleToolchainVersion},
			{path: "github.com/spice-framework/spice-agent", version: agentCoreDependencyVersion},
		},
	},
	"spice-agent-tools-coding": {
		repository: "spice-agent-tools-coding", module: "github.com/spice-framework/spice-agent-tools-coding",
		source: "https://github.com/spice-framework/spice-agent-tools-coding", version: agentCodingToolsReleaseVersion,
		metadataFile: "spice-release.json",
		requiredModules: []selectedModule{
			{path: "github.com/spice-framework/spice", version: historicalSpiceFoundationVersion},
			{path: "github.com/spice-framework/toolchain", version: historicalModuleToolchainVersion},
			{path: "github.com/spice-framework/spice-agent", version: agentCoreDependencyVersion},
		},
	},
	"spice-agent-tui": {
		repository: "spice-agent-tui", module: "github.com/spice-framework/spice-agent-tui",
		source: "https://github.com/spice-framework/spice-agent-tui", version: agentTUIReleaseVersion,
		metadataFile: "spice-release.json",
		requiredModules: []selectedModule{
			{path: "github.com/spice-framework/spice", version: spiceFoundationVersion},
			{path: "github.com/spice-framework/toolchain", version: toolchainDistributionVersion},
		},
	},
}

type releaseIntent struct {
	Schema     int    `json:"schema"`
	Profile    string `json:"profile"`
	Repository string `json:"repository"`
	Module     string `json:"module"`
	Version    string `json:"version"`
}

type releaseMetadata struct {
	Schema          int            `json:"schema"`
	Profile         string         `json:"profile"`
	Repository      string         `json:"repository"`
	Module          string         `json:"module"`
	Source          string         `json:"source"`
	Version         string         `json:"version"`
	Commit          string         `json:"commit"`
	SourceDateEpoch int64          `json:"source_date_epoch"`
	Go              string         `json:"go"`
	Artifacts       []artifactFact `json:"artifacts"`
}

type artifactFact struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int    `json:"size"`
}

type selectedModule struct {
	path    string
	version string
}
