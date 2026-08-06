package boundarygate

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const releaseTrustAnchorFingerprint = "9be4a0a3d312e48ccc1c17136510e7658c5d1fcda8f95ab2e938b6ffb0d97272"

func TestReleaseTrustAnchorIsCanonicalEd25519(t *testing.T) {
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
	workflow := string(content)
	for _, required := range []string{
		"permissions:\n  contents: read",
		"cancel-in-progress: false",
		"name: release-signing",
		"name: release-publish",
		"SIGNING_KEY_FILE_B64: ${{ secrets.SPICE_RELEASE_SIGNING_KEY_FILE_B64 }}",
		"go run -mod=vendor ./cmd/spice-release-verify",
		"gh release create",
		"gh release download",
		"gh release edit",
	} {
		if !strings.Contains(workflow, required) {
			t.Errorf("release workflow is missing %q", required)
		}
	}
	if count := strings.Count(workflow, "contents: write"); count != 1 {
		t.Errorf("release workflow grants contents:write %d times, require exactly one protected publisher", count)
	}
	if count := strings.Count(workflow, "SPICE_RELEASE_SIGNING_KEY_FILE_B64"); count != 1 {
		t.Errorf("release workflow references the signing secret %d times, require exactly one protected use", count)
	}
	if count := strings.Count(workflow, "go run -mod=vendor ./cmd/spice-release-verify"); count != 3 {
		t.Errorf("release workflow runs the independent verifier %d times, require pre-approval and two publish checks", count)
	}
	for _, forbidden := range []string{
		"secrets: inherit",
		"create-draft:",
		"verify-draft:",
	} {
		if strings.Contains(workflow, forbidden) {
			t.Errorf("release workflow contains forbidden authority path %q", forbidden)
		}
	}
	publish := strings.Index(workflow, "\n  publish:\n")
	write := strings.Index(workflow, "contents: write")
	if publish < 0 || write < publish {
		t.Error("contents:write is not scoped to the protected publish job")
	}
	signing := strings.Index(workflow, "\n  signed-build:\n")
	unsigned := strings.Index(workflow, "\n  unsigned-rebuild:\n")
	secret := strings.Index(workflow, "SIGNING_KEY_FILE_B64: ${{ secrets.")
	if signing < 0 || unsigned < 0 || secret < signing || secret > unsigned {
		t.Error("signing secret escaped the protected signed-build job")
	}
}
