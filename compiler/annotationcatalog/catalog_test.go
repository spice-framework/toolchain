package annotationcatalog

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCatalogEnvironmentIsNativeOfflineAndImmutable(t *testing.T) {
	t.Parallel()
	original := []string{
		"PATH=fixture",
		"CGO_ENABLED=1",
		"GOAMD64=v4",
		"GOARCH=wasm",
		"GOAUTH=netrc",
		"GOENV=hostile",
		"GOEXPERIMENT=ambientexperiment",
		"GOFLAGS=-tags=ambient",
		"GOOS=js",
		"GOPROXY=http://127.0.0.1:1",
		"GOSUMDB=invalid.example",
		"GOTOOLCHAIN=go1.99.0+auto",
	}
	got := catalogEnvironment(original)
	values := make(map[string]string, len(got))
	for _, entry := range got {
		name, value, found := strings.Cut(entry, "=")
		if found {
			values[strings.ToUpper(name)] = value
		}
	}
	wanted := map[string]string{
		"CGO_ENABLED":  "0",
		"GOARCH":       runtime.GOARCH,
		"GOAUTH":       "off",
		"GOENV":        "off",
		"GOEXPERIMENT": "",
		"GOFIPS140":    "off",
		"GOFLAGS":      "",
		"GOOS":         runtime.GOOS,
		"GOPROXY":      "off",
		"GOSUMDB":      "off",
		"GOTOOLCHAIN":  "local",
	}
	for name, value := range wanted {
		if values[name] != value {
			t.Fatalf("catalog environment %s = %q, want %q", name, values[name], value)
		}
	}
	if _, found := values["GOAMD64"]; found {
		t.Fatalf("catalog environment retained GOAMD64: %v", got)
	}
	if original[1] != "CGO_ENABLED=1" {
		t.Fatalf("catalogEnvironment() mutated input: %v", original)
	}
}

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
		"github.com/spice-framework/spice/annotation/core.Application",
	} {
		candidate, exists := found[identity]
		if !exists ||
			candidate.Tool == "" ||
			candidate.Handler == "" ||
			candidate.Protocol != "spice.annotation/v1alpha2" ||
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

require example.com/plugin v1.2.3
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

import "github.com/spice-framework/spice/annotation/sdk"

// Controller documents the vendored descriptor.
func Controller() sdk.Definition {
	return sdk.Definition{
		Name: "web.Controller",
		Summary: "Vendored controller.",
		Implementation: sdk.Implementation{
			Tool: "example.com/plugin/cmd/spice-annotations",
			Handler: ControllerHandler,
			Protocol: sdk.ProtocolV1Alpha2,
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

func TestDiscoverReadsWorkspaceModulesAndWorkspaceVendor(t *testing.T) {
	t.Parallel()
	workspace := t.TempDir()
	application := filepath.Join(workspace, "application")
	plugin := filepath.Join(workspace, "plugin")
	writeCatalogFile(t, workspace, "go.work", `go 1.26.0

use (
	./application
	./plugin
)
`)
	writeCatalogFile(t, application, "go.mod", `module example.com/application

go 1.26.0

tool (
	example.com/plugin/cmd/spice-annotations
	example.com/vendor-plugin/cmd/spice-annotations
)

require example.com/vendor-plugin v1.2.3

replace example.com/vendor-plugin v1.2.3 => example.com/vendor-fork v1.4.0
`)
	writeCatalogFile(t, plugin, "go.mod", `module example.com/plugin

go 1.26.0
`)
	writeCatalogFile(
		t,
		plugin,
		"annotation/local/descriptor.go",
		descriptorFixture("local.Component", "example.com/plugin/cmd/spice-annotations", "LocalHandler"),
	)
	writeCatalogFile(t, workspace, "vendor/modules.txt", `## workspace
# example.com/vendor-plugin v1.2.3 => example.com/vendor-fork v1.4.0
## explicit; go 1.26.0
example.com/vendor-plugin/annotation/remote
`)
	writeCatalogFile(
		t,
		workspace,
		"vendor/example.com/vendor-plugin/annotation/remote/descriptor.go",
		descriptorFixture("remote.Component", "example.com/vendor-plugin/cmd/spice-annotations", "RemoteHandler"),
	)

	candidates, err := Discover(
		context.Background(),
		application,
		[]string{"GOWORK=auto"},
	)
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	found := make(map[string]Candidate, len(candidates))
	for _, candidate := range candidates {
		found[candidate.Package+"."+candidate.Symbol] = candidate
	}
	for _, identity := range []string{
		"example.com/plugin/annotation/local.Descriptor",
		"example.com/vendor-plugin/annotation/remote.Descriptor",
	} {
		candidate, exists := found[identity]
		if !exists || !candidate.ToolAuthorized {
			t.Fatalf("candidate %q = %+v, found = %t", identity, candidate, exists)
		}
	}
	remote := found["example.com/vendor-plugin/annotation/remote.Descriptor"]
	if remote.Module != "example.com/vendor-plugin" ||
		remote.Version != "v1.2.3" ||
		remote.ReplacementModule != "example.com/vendor-fork" ||
		remote.ReplacementVersion != "v1.4.0" ||
		remote.LocalReplacement {
		t.Fatalf("remote replacement provenance = %+v", remote)
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

import "github.com/spice-framework/spice/annotation/sdk"

func Send() sdk.Definition {
	return sdk.Definition{
		Name: "mail.Send",
		Summary: "Sends mail.",
		Implementation: sdk.Implementation{
			Tool: "example.com/mail/cmd/spice-annotations",
			Handler: SendHandler,
			Protocol: sdk.ProtocolV1Alpha2,
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

import "github.com/spice-framework/spice/annotation/sdk"

func Hidden() sdk.Definition {
	return sdk.Definition{
		Name: "nested.Hidden",
		Implementation: sdk.Implementation{
			Tool: "example.com/nested/cmd/annotations",
			Handler: HiddenHandler,
			Protocol: sdk.ProtocolV1Alpha2,
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

func TestCatalogGoFileExcludesAllSpiceGeneratedUnits(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		want bool
	}{
		{name: "controller.go", want: true},
		{name: "controller_test.go", want: false},
		{name: "spice_contracts_gen.go", want: false},
		{name: "spice_http_route_deadbeef_gen.go", want: false},
		{name: "controller_spice_gen.go", want: false},
	}
	for _, test := range tests {
		if got := catalogGoFile(test.name); got != test.want {
			t.Errorf("catalogGoFile(%q) = %t, want %t", test.name, got, test.want)
		}
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

func descriptorFixture(name, tool, handler string) string {
	return `package annotation

import "github.com/spice-framework/spice/annotation/sdk"

func Descriptor() sdk.Definition {
	return sdk.Definition{
		Name: "` + name + `",
		Implementation: sdk.Implementation{
			Tool: "` + tool + `",
			Handler: ` + handler + `,
			Protocol: sdk.ProtocolV1Alpha2,
		},
	}
}
`
}
