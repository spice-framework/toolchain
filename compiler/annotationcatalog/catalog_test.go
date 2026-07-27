package annotationcatalog

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverFindsModuleGraphDescriptorsOffline(t *testing.T) {
	t.Parallel()
	root, err := filepath.Abs(filepath.Join(
		"..",
		"..",
		"testdata",
		"annotationapp",
	))
	if err != nil {
		t.Fatalf("resolve annotation fixture: %v", err)
	}
	candidates, err := Discover(context.Background(), root, nil)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	found := make(map[string]Candidate)
	for _, candidate := range candidates {
		found[candidate.Package+"."+candidate.Symbol] = candidate
	}
	for _, identity := range []string{
		"example.com/spice-annotation-fixture/annotation/policy.Policy",
		"example.com/spice-annotation-fixture/annotation/wiring.Factory",
		"github.com/StevenBuglione/spice/annotation/core.Application",
	} {
		candidate, exists := found[identity]
		if !exists ||
			candidate.Tool == "" ||
			candidate.Handler == "" ||
			candidate.Protocol != "spice.annotation/v1alpha1" ||
			!candidate.ToolAuthorized ||
			candidate.DescriptorPosition.Filename == "" {
			t.Fatalf("candidate %q = %+v, found = %t", identity, candidate, exists)
		}
	}
	fixture := found["example.com/spice-annotation-fixture/annotation/wiring.Factory"]
	if fixture.Module != "example.com/spice-annotation-fixture" ||
		fixture.Version != "v0.0.0" ||
		!fixture.LocalReplacement ||
		!strings.HasSuffix(
			filepath.ToSlash(fixture.ReplacementDir),
			"/testdata/annotationfixture",
		) {
		t.Fatalf("fixture provenance = %+v", fixture)
	}
}

func TestDiscoverReadsVendoredDescriptorCatalog(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeCatalogFile(t, root, "go.mod", `module example.com/application

go 1.26.0

tool example.com/plugin/cmd/spice-annotations
`)
	writeCatalogFile(t, root, "vendor/modules.txt", `# example.com/plugin v1.2.3
## explicit; go 1.26.0
example.com/plugin/annotation/web
`)
	writeCatalogFile(
		t,
		root,
		"vendor/example.com/plugin/annotation/web/controller.go",
		`package web

import "github.com/StevenBuglione/spice/annotation/sdk"

// Controller documents the vendored descriptor.
func Controller() sdk.Definition {
	return sdk.Definition{
		Name: "web.Controller",
		Summary: "Vendored controller.",
		Implementation: sdk.Implementation{
			Tool: "example.com/plugin/cmd/spice-annotations",
			Handler: "web/controller",
			Protocol: sdk.ProtocolV1Alpha1,
		},
	}
}
`,
	)
	candidates, err := Discover(context.Background(), root, nil)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(candidates) != 1 {
		t.Fatalf("Discover() = %+v", candidates)
	}
	got := candidates[0]
	if got.Package != "example.com/plugin/annotation/web" ||
		got.Symbol != "Controller" ||
		got.CanonicalName != "web.Controller" ||
		got.Module != "example.com/plugin" ||
		got.Version != "v1.2.3" ||
		!got.ToolAuthorized {
		t.Fatalf("candidate = %+v", got)
	}
}

func TestDiscoverRejectsNilContext(t *testing.T) {
	t.Parallel()
	if _, err := Discover(
		nil, //nolint:staticcheck // The public fail-closed context contract is under test.
		t.TempDir(),
		nil,
	); err == nil || !strings.Contains(err.Error(), "context") {
		t.Fatalf("Discover(nil) error = %v", err)
	}
}

func TestDiscoverSkipsIncompleteAndNestedDescriptorsWithoutAuthorization(
	t *testing.T,
) {
	t.Parallel()
	root := t.TempDir()
	writeCatalogFile(t, root, "go.mod", `module example.com/application

go 1.26.0
`)
	writeCatalogFile(
		t,
		root,
		"annotation/mail/send.go",
		`package mail

import "github.com/StevenBuglione/spice/annotation/sdk"

func Send() sdk.Definition {
	return sdk.Definition{
		Name: "mail.Send",
		Summary: "Sends mail.",
		Implementation: sdk.Implementation{
			Tool: "example.com/mail/cmd/spice-annotations",
			Handler: "mail/send",
			Protocol: sdk.ProtocolV1Alpha1,
		},
	}
}

func Incomplete() sdk.Definition {
	return sdk.Definition{Name: "mail.Incomplete"}
}
`,
	)
	writeCatalogFile(
		t,
		root,
		"nested/go.mod",
		"module example.com/nested\n\ngo 1.26.0\n",
	)
	writeCatalogFile(
		t,
		root,
		"nested/annotation/hidden.go",
		`package annotation

import "github.com/StevenBuglione/spice/annotation/sdk"

func Hidden() sdk.Definition {
	return sdk.Definition{
		Name: "nested.Hidden",
		Implementation: sdk.Implementation{
			Tool: "example.com/nested/cmd/annotations",
			Handler: "nested/hidden",
			Protocol: sdk.ProtocolV1Alpha1,
		},
	}
}
`,
	)
	candidates, err := Discover(context.Background(), root, nil)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(candidates) != 1 ||
		candidates[0].Symbol != "Send" ||
		candidates[0].ToolAuthorized ||
		candidates[0].DescriptorPosition.Filename == "" {
		t.Fatalf("Discover() = %+v", candidates)
	}
}

func writeCatalogFile(
	t *testing.T,
	root string,
	name string,
	content string,
) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}
