package goreleaseverify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	moduleFixtureVersion       = agentCoreReleaseVersion
	distributionFixtureVersion = "v0.1.0-preview.1"
	historicalSpiceVersion     = "v0.1.0-preview.1.0.20260806200749-524424a04df0"
)

type releaseFixture struct {
	root      string
	artifacts string
	config    Config
	files     []string
	runner    *fixtureGoRunner
}

type fixtureGoRunner struct {
	t               *testing.T
	callerRoot      string
	canonicalGoSum  []byte
	canonicalVendor map[string][]byte
	onDownload      func(string) error
}

func TestVerifyAcceptsExactIndependentContract(t *testing.T) {
	t.Parallel()
	fixture := newReleaseFixture(t, "")
	result, err := verifyFixture(t, fixture)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if result.Module != fixture.config.Module || result.Commit != fixture.config.Commit ||
		result.Profile != ProfileGoModule || !slices.Equal(result.Files, fixture.files) {
		t.Fatalf("Verify() = %#v", result)
	}
	output, _, err := readArtifactSet(fixture.config.VerifiedOutput, fixture.files)
	if err != nil {
		t.Fatalf("read verifier-owned output: %v", err)
	}
	input, _, err := readArtifactSet(fixture.artifacts, fixture.files)
	if err != nil || compareArtifactMaps(output, input, fixture.files) != nil {
		t.Fatalf("verifier-owned output differs from verified input: %v", err)
	}
}

func TestVerifyBuildsOnlyIsolatedExactGitSource(t *testing.T) {
	t.Parallel()
	fixture := newReleaseFixture(t, "")
	fixture.runner.onDownload = func(materializedRoot string) error {
		if filepath.Clean(materializedRoot) == filepath.Clean(fixture.root) {
			return errors.New("materialized root is caller worktree")
		}
		return os.WriteFile(filepath.Join(fixture.root, "ignored.go"), []byte("this is not valid Go"), 0o600)
	}
	if _, err := verifyFixture(t, fixture); err != nil {
		t.Fatalf("Verify(ignored caller mutation) error = %v", err)
	}
	status := strings.TrimSpace(runGit(t, fixture.root, "status", "--porcelain=v1", "--untracked-files=all"))
	if status != "" {
		t.Fatalf("ignored caller mutation unexpectedly changed Git status: %q", status)
	}
}

func TestVerifyAuthenticatesSumsAndRegeneratesVendor(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		variant string
		want    string
	}{
		{name: "fake sum", variant: "fake-sum", want: "checksum database"},
		{name: "altered vendor", variant: "altered-vendor", want: "vendor file set differs"},
		{name: "vendor mode drift", variant: "vendor-mode", want: "differs in bytes or mode"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newReleaseFixture(t, test.variant)
			if _, err := verifyFixture(t, fixture); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Verify(%s) error = %v, want %q", test.variant, err, test.want)
			}
		})
	}
	t.Run("checksum completion stays private", func(t *testing.T) {
		t.Parallel()
		fixture := newReleaseFixture(t, "")
		fixture.runner.onDownload = func(materializedRoot string) error {
			return os.WriteFile(
				filepath.Join(materializedRoot, authenticationSumFile),
				append(slices.Clone(fixture.runner.canonicalGoSum), []byte("new.example/module v1.0.0 h1:AAAA=\n")...),
				0o600,
			)
		}
		if _, err := verifyFixture(t, fixture); err != nil {
			t.Fatalf("Verify(private checksum completion) error = %v", err)
		}
	})
}

func TestVerifyRequiresAbsentOwnedOutputAndRelistsInput(t *testing.T) {
	t.Parallel()
	t.Run("existing output", func(t *testing.T) {
		t.Parallel()
		fixture := newReleaseFixture(t, "")
		if err := os.Mkdir(fixture.config.VerifiedOutput, 0o700); err != nil {
			t.Fatal(err)
		}
		if _, err := verifyFixture(t, fixture); err == nil || !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("Verify(existing output) error = %v", err)
		}
	})
	t.Run("late input extra", func(t *testing.T) {
		t.Parallel()
		fixture := newReleaseFixture(t, "")
		fixture.runner.onDownload = func(string) error {
			return os.WriteFile(filepath.Join(fixture.artifacts, "late-extra"), []byte("untrusted"), 0o600)
		}
		if _, err := verifyFixture(t, fixture); err == nil || !strings.Contains(err.Error(), "artifact set") {
			t.Fatalf("Verify(late extra) error = %v", err)
		}
		if _, err := os.Stat(fixture.config.VerifiedOutput); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("verified output exists after failed input relist: %v", err)
		}
	})
}

func TestCompiledPoliciesPinExactRequiredModuleVersions(t *testing.T) {
	t.Parallel()
	wantPolicies := map[string][]selectedModule{
		"spice": nil,
		"spice-agent": {
			{path: "github.com/spice-framework/spice", version: agentCoreSpiceVersion},
			{path: "github.com/spice-framework/toolchain", version: agentCoreToolchainVersion},
		},
		"spice-agent-provider-openai": {
			{path: "github.com/spice-framework/spice", version: historicalSpiceFoundationVersion},
			{path: "github.com/spice-framework/toolchain", version: historicalModuleToolchainVersion},
			{path: "github.com/spice-framework/spice-agent", version: agentCoreDependencyVersion},
		},
		"spice-agent-tools-coding": {
			{path: "github.com/spice-framework/spice", version: historicalSpiceFoundationVersion},
			{path: "github.com/spice-framework/toolchain", version: historicalModuleToolchainVersion},
			{path: "github.com/spice-framework/spice-agent", version: agentCoreDependencyVersion},
		},
		"spice-agent-tui": {
			{path: "github.com/spice-framework/spice", version: spiceFoundationVersion},
			{path: "github.com/spice-framework/toolchain", version: agentTUIToolchainVersion},
		},
	}
	if len(releasePolicies) != len(wantPolicies) {
		t.Fatalf("release policy count = %d, want %d", len(releasePolicies), len(wantPolicies))
	}
	for repository, want := range wantPolicies {
		policy, found := releasePolicies[repository]
		if !found {
			t.Errorf("policy %s is missing", repository)
			continue
		}
		if !slices.Equal(policy.requiredModules, want) {
			t.Errorf("policy %s required modules = %#v, want %#v", repository, policy.requiredModules, want)
		}
	}
}

func TestToolchainPreviewSevenPreservesEveryOtherReleaseAndHistoricalSelection(t *testing.T) {
	t.Parallel()
	if got := releasePolicies["spice"].version; got != "v0.1.0-preview.4" {
		t.Errorf("Spice recovery version = %q, want v0.1.0-preview.4", got)
	}
	wantToolchainModules := []selectedModule{{
		path: "github.com/spice-framework/spice", version: "v0.1.0-preview.4",
	}}
	toolchain := distributionPolicies["toolchain"]
	if toolchain.version != "v0.1.0-preview.7" || !slices.Equal(toolchain.requiredModules, wantToolchainModules) {
		t.Errorf("Toolchain policy = %#v, want preview.7 with modules %#v", toolchain, wantToolchainModules)
	}
	failedFoundation := selectedModule{
		path: "github.com/spice-framework/spice", version: "v0.1.0-preview.3",
	}
	if slices.Contains(toolchain.requiredModules, failedFoundation) {
		t.Errorf("Toolchain recovery policy retains failed foundation = %#v", toolchain.requiredModules)
	}
	wantTUIModules := []selectedModule{
		{path: "github.com/spice-framework/spice", version: "v0.1.0-preview.4"},
		{path: "github.com/spice-framework/toolchain", version: agentTUIToolchainVersion},
	}
	tui := releasePolicies["spice-agent-tui"]
	if tui.version != "v0.1.0-preview.2" || !slices.Equal(tui.requiredModules, wantTUIModules) {
		t.Errorf("TUI recovery policy = %#v, want preview.2 with modules %#v", tui, wantTUIModules)
	}
	if slices.Contains(tui.requiredModules, failedFoundation) {
		t.Errorf("TUI recovery policy retains failed foundation = %#v", tui.requiredModules)
	}
	wantAgentModules := []selectedModule{
		{path: "github.com/spice-framework/spice", version: agentCoreSpiceVersion},
		{path: "github.com/spice-framework/toolchain", version: agentCoreToolchainVersion},
	}
	if got := releasePolicies["spice-agent"].requiredModules; !slices.Equal(got, wantAgentModules) {
		t.Errorf("Agent required modules = %#v, want %#v", got, wantAgentModules)
	}
	wantCodingModules := []selectedModule{
		{path: "github.com/spice-framework/spice", version: historicalSpiceFoundationVersion},
		{path: "github.com/spice-framework/toolchain", version: historicalCodingToolchainVersion},
		{path: "github.com/spice-framework/spice-agent", version: agentCoreDependencyVersion},
		{path: "github.com/spice-framework/spice-agent-provider-openai", version: historicalCodingProviderVersion},
		{path: "github.com/spice-framework/spice-agent-tools-coding", version: historicalCodingToolsVersion},
		{path: "github.com/spice-framework/spice-agent-tui", version: historicalCodingTUIVersion},
	}
	if got := distributionPolicies["spice-agent-coding"].requiredModules; !slices.Equal(got, wantCodingModules) {
		t.Errorf("Coding required modules = %#v, want %#v", got, wantCodingModules)
	}
}

func TestCompiledPoliciesRejectStaleAgentSelections(t *testing.T) {
	t.Parallel()
	recovered := selectedModule{
		path:    "github.com/spice-framework/spice-agent",
		version: agentCoreDependencyVersion,
	}
	for _, repository := range []string{"spice-agent-provider-openai", "spice-agent-tools-coding"} {
		modules := releasePolicies[repository].requiredModules
		for _, staleVersion := range []string{
			"v0.1.0-preview.1",
			"v0.1.0-preview.2",
			"v0.1.0-preview.3",
		} {
			stale := selectedModule{path: "github.com/spice-framework/spice-agent", version: staleVersion}
			if slices.Contains(modules, stale) {
				t.Errorf("policy %s required modules = %#v, contains stale Agent %s", repository, modules, staleVersion)
			}
		}
		if !slices.Contains(modules, recovered) {
			t.Errorf("policy %s required modules = %#v, missing recovered Agent", repository, modules)
		}
	}
	modules := distributionPolicies["spice-agent-coding"].requiredModules
	for _, staleVersion := range []string{
		"v0.1.0-preview.1",
		"v0.1.0-preview.2",
		"v0.1.0-preview.3",
	} {
		stale := selectedModule{path: "github.com/spice-framework/spice-agent", version: staleVersion}
		if slices.Contains(modules, stale) {
			t.Errorf("distribution required modules = %#v, contains stale Agent %s", modules, staleVersion)
		}
	}
	if !slices.Contains(modules, recovered) {
		t.Errorf("distribution required modules = %#v, missing recovered Agent", modules)
	}
}

func TestCompiledPoliciesRetainExactReleaseVersions(t *testing.T) {
	t.Parallel()
	want := map[string]string{
		"spice":                       "v0.1.0-preview.4",
		"spice-agent":                 agentCoreReleaseVersion,
		"spice-agent-provider-openai": agentProviderReleaseVersion,
		"spice-agent-tools-coding":    agentCodingToolsReleaseVersion,
		"spice-agent-tui":             "v0.1.0-preview.2",
	}
	for repository, version := range want {
		if got := releasePolicies[repository].version; got != version {
			t.Errorf("policy %s version = %q, want %q", repository, got, version)
		}
	}
	wantDistributions := map[string]string{
		"toolchain":          "v0.1.0-preview.7",
		"spice-agent-coding": agentDistributionVersion,
	}
	if len(distributionPolicies) != len(wantDistributions) {
		t.Fatalf("distribution policy count = %d, want %d", len(distributionPolicies), len(wantDistributions))
	}
	for repository, version := range wantDistributions {
		if got := distributionPolicies[repository].version; got != version {
			t.Errorf("distribution %s version = %q, want %q", repository, got, version)
		}
	}
}

func TestAgentPreviewSevenAndHistoricalPoliciesPreserveEveryDependencyPin(t *testing.T) {
	t.Parallel()
	want := map[string]string{
		"Agent core Spice":            "v0.1.0-preview.4",
		"Agent core Toolchain":        "v0.1.0-preview.2",
		"Agent core release":          "v0.1.0-preview.7",
		"historical Spice":            "v0.1.0-preview.2",
		"historical module Toolchain": "v0.1.0-preview.1.0.20260806203056-d0b9ac086bd6",
		"historical Coding Toolchain": "v0.1.0-preview.1.0.20260807044408-6598abca8196",
		"Provider release":            "v0.1.0-preview.1",
		"coding-tools release":        "v0.1.0-preview.1",
		"historical Coding Provider":  "v0.1.0-preview.1",
		"historical Coding Tools":     "v0.1.0-preview.1",
		"historical Coding TUI":       "v0.1.0-preview.1",
		"Agent dependency":            "v0.1.0-preview.4",
		"Agent distribution":          "v0.1.0-preview.4",
		"Agent TUI Toolchain":         "v0.1.0-preview.4",
	}
	actual := map[string]string{
		"Agent core Spice":            agentCoreSpiceVersion,
		"Agent core Toolchain":        agentCoreToolchainVersion,
		"Agent core release":          agentCoreReleaseVersion,
		"historical Spice":            historicalSpiceFoundationVersion,
		"historical module Toolchain": historicalModuleToolchainVersion,
		"historical Coding Toolchain": historicalCodingToolchainVersion,
		"Provider release":            agentProviderReleaseVersion,
		"coding-tools release":        agentCodingToolsReleaseVersion,
		"historical Coding Provider":  historicalCodingProviderVersion,
		"historical Coding Tools":     historicalCodingToolsVersion,
		"historical Coding TUI":       historicalCodingTUIVersion,
		"Agent dependency":            agentCoreDependencyVersion,
		"Agent distribution":          agentDistributionVersion,
		"Agent TUI Toolchain":         agentTUIToolchainVersion,
	}
	for name, version := range want {
		if actual[name] != version {
			t.Errorf("%s version = %q, want %q", name, actual[name], version)
		}
	}
}

func TestVerifyRejectsArtifactAndSourceDrift(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*testing.T, *releaseFixture)
		want   string
	}{
		{
			name: "extra artifact",
			mutate: func(t *testing.T, fixture *releaseFixture) {
				t.Helper()
				writeFile(t, filepath.Join(fixture.artifacts, "unexpected"), []byte("data"))
			},
			want: "artifact set",
		},
		{
			name: "missing artifact",
			mutate: func(t *testing.T, fixture *releaseFixture) {
				t.Helper()
				if err := os.Remove(filepath.Join(fixture.artifacts, fixture.files[1])); err != nil {
					t.Fatal(err)
				}
			},
			want: "artifact set",
		},
		{
			name: "tampered checksum subject",
			mutate: func(t *testing.T, fixture *releaseFixture) {
				t.Helper()
				name := artifactName(fixture, "_source.tar.gz")
				writeFile(t, filepath.Join(fixture.artifacts, name), []byte("tampered"))
			},
			want: "SHA-256 mismatch",
		},
		{
			name: "nonreproducible archive",
			mutate: func(t *testing.T, fixture *releaseFixture) {
				t.Helper()
				name := artifactName(fixture, "_source.tar.gz")
				writeFile(t, filepath.Join(fixture.artifacts, name), []byte("tampered"))
				rewriteChecksums(t, fixture)
			},
			want: "not byte-reproducible",
		},
		{
			name: "changed sbom",
			mutate: func(t *testing.T, fixture *releaseFixture) {
				t.Helper()
				name := artifactName(fixture, "_sbom.spdx.json")
				writeFile(t, filepath.Join(fixture.artifacts, name), []byte("{}\n"))
				rewriteChecksums(t, fixture)
			},
			want: "SPDX SBOM does not exactly match",
		},
		{
			name: "duplicate metadata key",
			mutate: func(t *testing.T, fixture *releaseFixture) {
				t.Helper()
				name := artifactName(fixture, "_release.json")
				writeFile(t, filepath.Join(fixture.artifacts, name), []byte(`{"schema":1,"schema":1}`+"\n"))
				rewriteChecksums(t, fixture)
			},
			want: "repeats key",
		},
		{
			name: "dirty checkout",
			mutate: func(t *testing.T, fixture *releaseFixture) {
				t.Helper()
				writeFile(t, filepath.Join(fixture.root, "untracked"), []byte("dirty"))
			},
			want: "must be clean",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newReleaseFixture(t, "")
			test.mutate(t, fixture)
			if _, err := verifyFixture(t, fixture); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Verify() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestVerifyRejectsSymlinkArtifactsWhenSupported(t *testing.T) {
	t.Parallel()
	fixture := newReleaseFixture(t, "")
	target := filepath.Join(fixture.artifacts, fixture.files[1])
	link := filepath.Join(fixture.artifacts, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if _, err := verifyFixture(t, fixture); err == nil || !strings.Contains(err.Error(), "bounded regular file") {
		t.Fatalf("Verify(symlink) error = %v", err)
	}
}

func TestVerifyRejectsCommittedSymlink(t *testing.T) {
	t.Parallel()
	fixture := newReleaseFixture(t, "symlink")
	if _, err := verifyFixture(t, fixture); err == nil ||
		!strings.Contains(err.Error(), "symlinks are not permitted") {
		t.Fatalf("Verify(committed symlink) error = %v", err)
	}
}

func TestVerifyRejectsPolicyAndModuleViolations(t *testing.T) {
	t.Parallel()
	fixture := newReleaseFixture(t, "")
	tests := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{name: "distribution", mutate: func(value *Config) { value.Profile = "go-distribution-v1" }, want: "unsupported"},
		{name: "starter", mutate: func(value *Config) { value.RepositoryName = "starter-mail" }, want: "starter repositories"},
		{name: "unknown", mutate: func(value *Config) { value.RepositoryName = "spice-agent-unknown" }, want: "not independently authorized"},
		{name: "source", mutate: func(value *Config) { value.CanonicalSource += "-fork" }, want: "do not match"},
		{name: "module", mutate: func(value *Config) { value.Module += "/fork" }, want: "do not match"},
		{name: "stale Agent preview.1 release", mutate: func(value *Config) { value.Version = "v0.1.0-preview.1" }, want: "do not match"},
		{name: "stale Agent preview.2 release", mutate: func(value *Config) { value.Version = "v0.1.0-preview.2" }, want: "do not match"},
		{name: "stale Agent preview.3 release", mutate: func(value *Config) { value.Version = "v0.1.0-preview.3" }, want: "do not match"},
		{name: "stale Agent preview.4 release", mutate: func(value *Config) { value.Version = "v0.1.0-preview.4" }, want: "do not match"},
		{name: "stale Agent preview.5 release", mutate: func(value *Config) { value.Version = "v0.1.0-preview.5" }, want: "do not match"},
		{name: "stale Agent preview.6 release", mutate: func(value *Config) { value.Version = "v0.1.0-preview.6" }, want: "do not match"},
		{name: "version", mutate: func(value *Config) { value.Version = "v0.1.0" }, want: "do not match"},
		{name: "commit", mutate: func(value *Config) { value.Commit = strings.ToUpper(value.Commit) }, want: "lowercase"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			config := fixture.config
			test.mutate(&config)
			if _, err := verify(t.Context(), config, fixture.runner); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Verify() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestVerifyAcceptsToolDependencyMarkedIndirect(t *testing.T) {
	t.Parallel()
	fixture := newReleaseFixture(t, "indirect")
	if _, err := verifyFixture(t, fixture); err != nil {
		t.Fatalf("Verify(indirect tool dependency) error = %v", err)
	}
}

func TestVerifyRejectsMissingPolicyModuleAndReplacement(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		variant string
		want    string
	}{
		{name: "missing", variant: "missing", want: "does not require policy module"},
		{name: "replace", variant: "replace", want: "must not contain replace directives"},
		{name: "old Spice version", variant: "old-spice-version", want: "independent policy requires"},
		{name: "wrong version", variant: "wrong-version", want: "independent policy requires"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fixture := newReleaseFixture(t, test.variant)
			if _, err := verifyFixture(t, fixture); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Verify() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestVerifyRejectsCancellationAndNilContext(t *testing.T) {
	t.Parallel()
	fixture := newReleaseFixture(t, "")
	//nolint:staticcheck // A nil context is part of this public failure-boundary contract.
	if _, err := Verify(nil, fixture.config); err == nil || !strings.Contains(err.Error(), "context is nil") {
		t.Fatalf("Verify(nil) error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Verify(ctx, fixture.config); err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("Verify(canceled) error = %v", err)
	}
}

func TestTrustedGoEnvironmentDisablesCredentialsCGOAndAmbientConfiguration(t *testing.T) {
	t.Parallel()
	workspace := isolatedWorkspace{
		moduleCache: "module-cache",
		buildCache:  "build-cache",
		goPath:      "go-path",
		temporary:   "temporary",
	}
	actual := trustedGoEnvironment([]string{
		"PATH=trusted-path",
		"CGO_ENABLED=1",
		"GOAUTH=netrc",
		"GOENV=ambient",
		"GOFLAGS=-tags=unsafe",
		"GONOSUMDB=*",
		"GOPRIVATE=*",
		"GOPROXY=https://example.invalid",
		"GOSUMDB=sum.example.invalid",
		"GOTELEMETRY=local",
		"GOTOOLCHAIN=auto",
		"GOWORK=ambient.work",
		"GOPATH=ambient-go-path",
		"GOTMPDIR=ambient-temp",
		"HTTPS_PROXY=https://credential@example.invalid",
		"NETRC=secret",
	}, workspace, true)
	runner := fixtureGoRunner{t: t}
	if err := runner.requireEnvironment(actual, true); err != nil {
		t.Fatalf("trustedGoEnvironment(online) error = %v", err)
	}
	offline := trustedGoEnvironment(actual, workspace, false)
	if err := runner.requireEnvironment(offline, false); err != nil {
		t.Fatalf("trustedGoEnvironment(offline) error = %v", err)
	}
}

func TestCommandHelpersRejectMissingArguments(t *testing.T) {
	t.Parallel()
	if _, err := (systemGoRunner{}).Output(t.Context(), t.TempDir(), os.Environ()); err == nil ||
		!strings.Contains(err.Error(), "requires an argument") {
		t.Fatalf("goOutput() error = %v", err)
	}
	if _, err := (systemGoRunner{}).Output(t.Context(), t.TempDir(), os.Environ(), "version"); err == nil ||
		!strings.Contains(err.Error(), "not bound") {
		t.Fatalf("unbound go Output() error = %v", err)
	}
	bound, err := newSystemGoRunner()
	if err != nil {
		t.Fatalf("newSystemGoRunner() error = %v", err)
	}
	if !filepath.IsAbs(bound.executable) {
		t.Fatalf("newSystemGoRunner() executable = %q", bound.executable)
	}
	if _, err := gitOutput(t.Context(), t.TempDir(), maxDiagnostic); err == nil ||
		!strings.Contains(err.Error(), "requires an argument") {
		t.Fatalf("gitOutput() error = %v", err)
	}
}

func newReleaseFixture(t *testing.T, variant string) *releaseFixture {
	t.Helper()
	policy := releasePolicies["spice-agent"]
	fixtureSpiceVersion := policy.requiredModules[0].version
	if variant == "old-spice-version" {
		fixtureSpiceVersion = historicalSpiceVersion
	}
	parent := t.TempDir()
	root := filepath.Join(parent, "repository")
	artifacts := filepath.Join(parent, "artifacts")
	if err := os.MkdirAll(filepath.Join(root, "vendor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(artifacts, 0o755); err != nil {
		t.Fatal(err)
	}
	requirementSuffix := ""
	replacement := ""
	toolchainRequirement := "github.com/spice-framework/toolchain " + agentCoreToolchainVersion
	if variant == "indirect" {
		toolchainRequirement += " // indirect"
	}
	if variant == "missing" {
		toolchainRequirement = ""
	}
	if variant == "replace" {
		replacement = "\nreplace github.com/spice-framework/spice => github.com/spice-framework/spice " + fixtureSpiceVersion + "\n"
	}
	if variant == "wrong-version" {
		toolchainRequirement = "github.com/spice-framework/toolchain v0.1.0-preview.1"
	}
	writeFile(t, filepath.Join(root, "go.mod"), []byte(fmt.Sprintf(`module github.com/spice-framework/spice-agent

go 1.26.0

toolchain go1.26.5

require (
	github.com/spice-framework/spice %s%s
	%s
)
	%s`, fixtureSpiceVersion, requirementSuffix, toolchainRequirement, replacement)))
	sum := "h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	canonicalGoSum := []byte(
		"github.com/spice-framework/spice " + fixtureSpiceVersion + "/go.mod " + sum + "\n" +
			"github.com/spice-framework/toolchain " + agentCoreToolchainVersion + "/go.mod " + sum + "\n",
	)
	committedGoSum := slices.Clone(canonicalGoSum)
	if variant == "fake-sum" {
		committedGoSum = bytes.Replace(committedGoSum, []byte(sum), []byte("h1:BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB="), 1)
	}
	writeFile(t, filepath.Join(root, "go.sum"), committedGoSum)
	canonicalVendor := []byte(
		"# github.com/spice-framework/spice " + fixtureSpiceVersion + "\n## explicit; go 1.26.0\n" +
			"# github.com/spice-framework/toolchain " + agentCoreToolchainVersion + "\n## explicit; go 1.26.0\n",
	)
	writeFile(t, filepath.Join(root, "vendor", "modules.txt"), canonicalVendor)
	canonicalVendorFiles := map[string][]byte{"modules.txt": canonicalVendor}
	if variant == "altered-vendor" {
		writeFile(t, filepath.Join(root, "vendor", "unexpected.go"), []byte("package compromised\n"))
	}
	if variant == "vendor-mode" {
		content := []byte("verified vendor executable\n")
		writeFile(t, filepath.Join(root, "vendor", "tool.sh"), content)
		canonicalVendorFiles["tool.sh"] = content
	}
	writeFile(t, filepath.Join(root, "LICENSE"), []byte("Apache-2.0\n"))
	writeFile(t, filepath.Join(root, "README.md"), []byte("# fixture\n"))
	writeFile(t, filepath.Join(root, "main.go"), []byte("package fixture\n\nfunc Ready() bool { return true }\n"))
	writeFile(t, filepath.Join(root, ".gitignore"), []byte("ignored.go\n"))
	if variant == "symlink" {
		writeFile(t, filepath.Join(root, "latest"), []byte("README.md"))
	}
	writeFile(t, filepath.Join(root, "spice-release.json"), []byte(fmt.Sprintf(`{
  "schema": 1,
  "profile": "go-module-v1",
  "repository": "spice-agent",
  "module": "github.com/spice-framework/spice-agent",
  "version": %q
}
`, policy.version)))
	runGit(t, root, "init", "-q")
	runGit(t, root, "config", "user.name", "Spice Test")
	runGit(t, root, "config", "user.email", "test@spice.invalid")
	runGit(t, root, "remote", "add", "origin", "https://github.com/spice-framework/spice-agent.git")
	runGit(t, root, "add", ".")
	if variant == "vendor-mode" {
		runGit(t, root, "update-index", "--chmod=+x", "vendor/tool.sh")
		if runtime.GOOS != "windows" {
			if err := os.Chmod(filepath.Join(root, "vendor", "tool.sh"), 0o700); err != nil {
				t.Fatal(err)
			}
		}
	}
	if variant == "symlink" {
		objectID := strings.TrimSpace(runGit(t, root, "hash-object", "-w", "latest"))
		runGit(t, root, "update-index", "--add", "--cacheinfo", "120000,"+objectID+",latest")
		if runtime.GOOS != "windows" {
			if err := os.Remove(filepath.Join(root, "latest")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("README.md", filepath.Join(root, "latest")); err != nil {
				t.Fatal(err)
			}
		}
	}
	runGitEnv(t, root, []string{
		"GIT_AUTHOR_DATE=2026-08-07T00:00:00Z",
		"GIT_COMMITTER_DATE=2026-08-07T00:00:00Z",
	}, "commit", "-q", "-m", "fixture")
	runGit(t, root, "tag", moduleFixtureVersion)
	commit := strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD"))
	config := Config{
		Directory: artifacts, Repository: root, RepositoryName: policy.repository,
		CanonicalSource: policy.source, Module: policy.module, Version: policy.version,
		Commit: commit, Profile: ProfileGoModule,
		VerifiedOutput: filepath.Join(parent, "verified-output"),
	}
	epoch := time.Date(2026, time.August, 7, 0, 0, 0, 0, time.UTC)
	source := sourceIdentity{root: root, commit: commit, epoch: epoch}
	archive, err := expectedSourceArchive(t.Context(), source, policy.repository, policy.version)
	if err != nil {
		t.Fatal(err)
	}
	modules := []selectedModule{
		{path: "github.com/spice-framework/spice", version: fixtureSpiceVersion},
		{path: "github.com/spice-framework/toolchain", version: agentCoreToolchainVersion},
	}
	sbom, err := marshalCanonical(expectedSBOM(policy, commit, epoch, modules))
	if err != nil {
		t.Fatal(err)
	}
	base := "spice-agent_" + strings.TrimPrefix(policy.version, "v")
	archiveName := base + "_source.tar.gz"
	sbomName := base + "_sbom.spdx.json"
	metadataName := base + "_release.json"
	metadata, err := marshalCanonical(releaseMetadata{
		Schema: artifactSchema, Profile: ProfileGoModule, Repository: policy.repository,
		Module: policy.module, Source: policy.source, Version: policy.version, Commit: commit,
		SourceDateEpoch: epoch.Unix(), Go: "1.26.5",
		Artifacts: []artifactFact{artifactFactFor(sbomName, sbom), artifactFactFor(archiveName, archive)},
	})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(artifacts, archiveName), archive)
	writeFile(t, filepath.Join(artifacts, sbomName), sbom)
	writeFile(t, filepath.Join(artifacts, metadataName), metadata)
	fixture := &releaseFixture{
		root: root, artifacts: artifacts, config: config,
		runner: &fixtureGoRunner{
			t: t, callerRoot: root, canonicalGoSum: canonicalGoSum,
			canonicalVendor: canonicalVendorFiles,
		},
	}
	fixture.files = expectedAssetNames(policy)
	rewriteChecksums(t, fixture)
	return fixture
}

func rewriteChecksums(t *testing.T, fixture *releaseFixture) {
	t.Helper()
	names := make([]string, 0, len(fixture.files)-1)
	for _, name := range fixture.files {
		if name != "checksums.txt" {
			names = append(names, name)
		}
	}
	var content strings.Builder
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(fixture.artifacts, name))
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(data)
		fmt.Fprintf(&content, "%s  %s\n", hex.EncodeToString(digest[:]), name)
	}
	writeFile(t, filepath.Join(fixture.artifacts, "checksums.txt"), []byte(content.String()))
}

func artifactName(fixture *releaseFixture, suffix string) string {
	for _, name := range fixture.files {
		if strings.HasSuffix(name, suffix) {
			return name
		}
	}
	panic("artifact not found: " + suffix)
}

func verifyFixture(t *testing.T, fixture *releaseFixture) (Result, error) {
	t.Helper()
	return verify(t.Context(), fixture.config, fixture.runner)
}

func (runner *fixtureGoRunner) Output(
	_ context.Context,
	root string,
	environment []string,
	arguments ...string,
) ([]byte, error) {
	runner.t.Helper()
	if root == runner.callerRoot || filepath.Clean(root) == filepath.Clean(runner.callerRoot) {
		return nil, errors.New("Go command executed in caller worktree")
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("isolated source unexpectedly contains Git metadata")
	}
	command := strings.Join(arguments, " ")
	switch command {
	case "version":
		return []byte("go version go1.26.5 fixture\n"), nil
	case "mod download -modfile=" + authenticationModFile + " all":
		if err := runner.requireEnvironment(environment, true); err != nil {
			return nil, err
		}
		actual, err := os.ReadFile(filepath.Join(root, authenticationSumFile))
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(actual, runner.canonicalGoSum) {
			return nil, errors.New("public checksum database rejected fake go.sum")
		}
		if runner.onDownload != nil {
			if err := runner.onDownload(root); err != nil {
				return nil, err
			}
		}
		return nil, nil
	case "mod verify":
		return nil, runner.requireEnvironment(environment, true)
	case "list -mod=vendor ./...", "build -mod=vendor -trimpath ./...":
		if err := runner.requireEnvironment(environment, false); err != nil {
			return nil, err
		}
		if _, err := os.Stat(filepath.Join(root, "ignored.go")); !errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("caller ignored file reached isolated source")
		}
		return nil, nil
	}
	if len(arguments) == 5 && arguments[0] == "mod" && arguments[1] == "vendor" &&
		arguments[2] == "-modfile="+authenticationModFile && arguments[3] == "-o" {
		if err := runner.requireEnvironment(environment, true); err != nil {
			return nil, err
		}
		output := arguments[4]
		if err := os.Mkdir(output, 0o700); err != nil {
			return nil, err
		}
		for name, content := range runner.canonicalVendor {
			filename := filepath.Join(output, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(filename), 0o700); err != nil {
				return nil, err
			}
			if err := os.WriteFile(filename, content, 0o600); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}
	return nil, fmt.Errorf("unexpected fixture Go command %q", command)
}

func (runner *fixtureGoRunner) requireEnvironment(environment []string, online bool) error {
	values := make(map[string]string, len(environment))
	for _, item := range environment {
		key, value, found := strings.Cut(item, "=")
		if found {
			values[strings.ToUpper(key)] = value
		}
	}
	wantProxy := "off"
	wantSum := "off"
	if online {
		wantProxy = "https://proxy.golang.org"
		wantSum = "sum.golang.org"
	}
	if values["CGO_ENABLED"] != "0" || values["GOAUTH"] != "off" ||
		values["GOPROXY"] != wantProxy || values["GOSUMDB"] != wantSum ||
		values["GOPRIVATE"] != "none" || values["GONOPROXY"] != "none" ||
		values["GONOSUMDB"] != "none" || values["GOWORK"] != "off" ||
		values["GOTOOLCHAIN"] != "local" || values["GOTELEMETRY"] != "off" ||
		values["GOMODCACHE"] == "" || values["GOCACHE"] == "" || values["GOPATH"] == "" ||
		values["GOTMPDIR"] == "" || values["TEMP"] == "" || values["TMP"] == "" ||
		values["TMPDIR"] == "" {
		return fmt.Errorf("fixture Go environment is not fail-closed: %#v", values)
	}
	for _, forbidden := range []string{
		"ALL_PROXY", "HTTP_PROXY", "HTTPS_PROXY", "NETRC", "NO_PROXY", "SSH_AUTH_SOCK",
	} {
		if _, found := values[forbidden]; found {
			return fmt.Errorf("fixture Go environment retained forbidden %s", forbidden)
		}
	}
	return nil
}

func writeFile(t *testing.T, name string, content []byte) {
	t.Helper()
	if err := os.WriteFile(name, content, 0o600); err != nil {
		t.Fatal(err)
	}
}

func runGit(t *testing.T, root string, arguments ...string) string {
	t.Helper()
	return runGitEnv(t, root, nil, arguments...)
}

func runGitEnv(t *testing.T, root string, environment []string, arguments ...string) string {
	t.Helper()
	hermeticArguments := append(
		[]string{"-c", "commit.gpgsign=false", "-c", "tag.gpgsign=false"},
		arguments...,
	)
	command := exec.CommandContext(t.Context(), "git", hermeticArguments...)
	command.Dir = root
	command.Env = append(os.Environ(), environment...)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(arguments, " "), err, stderr.String())
	}
	return stdout.String()
}
