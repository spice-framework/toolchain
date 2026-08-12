package boundarygate

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const releaseTrustAnchorFingerprint = "9be4a0a3d312e48ccc1c17136510e7658c5d1fcda8f95ab2e938b6ffb0d97272"

func TestHistoricalReleaseTrustAnchorIsCanonicalEd25519(t *testing.T) {
	t.Parallel()
	content, err := os.ReadFile(filepath.Join("..", "..", "security", "release", "ed25519-public.pem"))
	if err != nil {
		t.Fatal(err)
	}
	block, rest := pem.Decode(content)
	if block == nil || block.Type != "PUBLIC KEY" || len(rest) != 0 {
		t.Fatal("release trust anchor is not one canonical PUBLIC KEY PEM block")
	}
	parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	key, ok := parsed.(ed25519.PublicKey)
	if !ok || len(key) != ed25519.PublicKeySize {
		t.Fatalf("release trust anchor has type %T and length %d", parsed, len(key))
	}
	canonical := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: block.Bytes})
	if !bytes.Equal(content, canonical) {
		t.Fatal("release trust anchor PEM is not canonical")
	}
	fingerprint := sha256.Sum256(block.Bytes)
	if got := hex.EncodeToString(fingerprint[:]); got != releaseTrustAnchorFingerprint {
		t.Fatalf("release trust anchor fingerprint = %s, want %s", got, releaseTrustAnchorFingerprint)
	}
}

func TestProductionReleaseWorkflowKeepsAuthorityBehindProtectedBoundaries(t *testing.T) {
	t.Parallel()
	content, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "release.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if err = validateProductionReleaseWorkflow(string(content)); err != nil {
		t.Fatal(err)
	}
}

func TestProductionReleaseWorkflowRejectsAuthorityDrift(t *testing.T) {
	t.Parallel()
	valid := expectedProductionReleaseWorkflow()
	for _, test := range []struct {
		name   string
		mutate func(string) string
	}{
		{name: "stale reusable pin", mutate: func(value string) string {
			return strings.Replace(value, productionWorkflowCommit, "a56c451168aae0f2b3075782156d204d75fb7f69", 2)
		}},
		{name: "mismatched repeated pin", mutate: func(value string) string {
			index := strings.LastIndex(value, productionWorkflowCommit)
			return value[:index] + strings.Repeat("b", 40) + value[index+len(productionWorkflowCommit):]
		}},
		{name: "workflow default permission", mutate: func(value string) string {
			return strings.Replace(value, "permissions: {}", "permissions:\n  contents: read", 1)
		}},
		{name: "permission drift", mutate: func(value string) string {
			return strings.Replace(value, "      id-token: write\n", "", 1)
		}},
		{name: "secret inheritance", mutate: func(value string) string {
			return strings.Replace(value, "    with:\n", "    secrets: inherit\n    with:\n", 1)
		}},
		{name: "local runner", mutate: func(value string) string {
			return strings.Replace(value, "    name: Keylessly attest and publish\n", "    name: Keylessly attest and publish\n    runs-on: ubuntu-latest\n", 1)
		}},
		{name: "local steps", mutate: func(value string) string {
			return strings.Replace(value, "    uses: spice-framework/", "    steps:\n      - run: echo bypass\n    uses: spice-framework/", 1)
		}},
		{name: "legacy signing", mutate: func(value string) string {
			return value + "\n# release-signing SPICE_RELEASE_SIGNING_KEY_FILE_B64\n"
		}},
		{name: "broad tag trigger", mutate: func(value string) string {
			return strings.Replace(value, `"v[0-9]*.[0-9]*.[0-9]*"`, `"v*"`, 1)
		}},
		{name: "wrong module", mutate: func(value string) string {
			return strings.Replace(value, "module: github.com/spice-framework/toolchain", "module: example.com/toolchain", 1)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := validateProductionReleaseWorkflow(test.mutate(valid)); err == nil {
				t.Fatal("mutated release workflow succeeded")
			}
		})
	}
}

const productionWorkflowCommit = "d84b2cbce217e2d259ee81727fb98b0c2db1656e"

func validateProductionReleaseWorkflow(workflow string) error {
	if workflow != expectedProductionReleaseWorkflow() {
		return errors.New("production release workflow differs from the reviewed reusable caller")
	}
	if strings.Count(workflow, productionWorkflowCommit) != 2 {
		return errors.New("production release workflow must pin the reviewed authority twice")
	}
	for _, forbidden := range []string{
		"runs-on:", "steps:", "secrets:", "secrets: inherit", "release-signing",
		"SPICE_RELEASE_SIGNING_KEY_FILE_B64", "cmd/spice-release", "cmd/spice-release-verify",
	} {
		if strings.Contains(workflow, forbidden) {
			return errors.New("production release workflow contains forbidden local or legacy authority")
		}
	}
	return nil
}

func expectedProductionReleaseWorkflow() string {
	return `name: Release

on:
  push:
    tags:
      - "v[0-9]*.[0-9]*.[0-9]*"

permissions: {}

jobs:
  release:
    name: Keylessly attest and publish
    permissions:
      contents: write
      id-token: write
      attestations: write
      artifact-metadata: write
    uses: spice-framework/.github/.github/workflows/go-distribution-release.yml@d84b2cbce217e2d259ee81727fb98b0c2db1656e
    with:
      module: github.com/spice-framework/toolchain
      workflow_commit: d84b2cbce217e2d259ee81727fb98b0c2db1656e
`
}
