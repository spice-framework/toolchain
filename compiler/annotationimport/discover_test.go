package annotationimport

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/spice-framework/spice/annotation"
)

func TestDiscoverFindsStableImportsAndHonorsOverlays(t *testing.T) {
	root := t.TempDir()
	writeDiscoveryFile(t, root, "app/main.go", `package app

const ignored = "// @import { Fake } from \"example.com/fake\""

// @import { Application } from "example.com/core"
`)
	writeDiscoveryFile(t, root, "app/other.go", `package app

// @import * as web from "example.com/old"
`)
	writeDiscoveryFile(t, root, "app/other_test.go", `package app

// @import { Test } from "example.com/test"
`)
	writeDiscoveryFile(t, root, "vendor/example.com/x/x.go", `package x

// @import { Vendor } from "example.com/vendor"
`)
	other := filepath.Join(root, "app", "other.go")
	discovery, err := Discover(root, map[string][]byte{
		other: []byte(`package app

// @import { Controller, Get as GET } from "example.com/web"
`),
	})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if want := []string{"example.com/core", "example.com/web"}; !reflect.DeepEqual(
		discovery.Packages,
		want,
	) {
		t.Fatalf("packages = %#v, want %#v", discovery.Packages, want)
	}
	wantReferences := []annotation.DefinitionReference{
		{Package: "example.com/core", Symbol: "Application"},
		{Package: "example.com/web", Symbol: "Controller"},
		{Package: "example.com/web", Symbol: "Get"},
	}
	if !reflect.DeepEqual(discovery.References, wantReferences) {
		t.Fatalf(
			"references = %#v, want %#v",
			discovery.References,
			wantReferences,
		)
	}
}

func TestDiscoverIncludesNewOverlaySourceInsideRoot(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "new.go")
	discovery, err := Discover(root, map[string][]byte{
		file: []byte(`package app
// @import { Application } from "example.com/core"
`),
	})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if len(discovery.Directives) != 1 ||
		discovery.Directives[0].Position.Filename != file {
		t.Fatalf("discovery = %#v", discovery)
	}
}

func TestDiscoverExcludesNestedModuleSourcesAndOverlays(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeDiscoveryFile(t, root, "app.go", `package app
// @import { Application } from "example.com/core"
`)
	writeDiscoveryFile(t, root, "editor/build/project/go.mod", `module example.com/editor
`)
	writeDiscoveryFile(t, root, "editor/build/project/fixture.go", `package fixture
// @import * as contracts from "example.com/unrelated/contracts"
`)
	overlayFile := filepath.Join(
		root,
		"editor",
		"build",
		"project",
		"overlay.go",
	)
	discovery, err := Discover(root, map[string][]byte{
		overlayFile: []byte(`package fixture
// @import { Other } from "example.com/unrelated/other"
`),
	})
	if err != nil {
		t.Fatalf("Discover() error = %v", err)
	}
	if want := []string{"example.com/core"}; !reflect.DeepEqual(
		discovery.Packages,
		want,
	) {
		t.Fatalf("packages = %#v, want %#v", discovery.Packages, want)
	}
}

func TestDiscoveryNamespacePackagesAreStableAndUnique(t *testing.T) {
	t.Parallel()
	discovery := Discovery{Directives: []annotation.ImportDirective{
		{
			Kind:    annotation.ImportNamespace,
			Package: "example.com/z",
		},
		{
			Kind:    annotation.ImportNamed,
			Package: "example.com/named",
		},
		{
			Kind:    annotation.ImportNamespace,
			Package: "example.com/a",
		},
		{
			Kind:    annotation.ImportNamespace,
			Package: "example.com/z",
		},
	}}
	want := []string{"example.com/a", "example.com/z"}
	if got := discovery.NamespacePackages(); !reflect.DeepEqual(got, want) {
		t.Fatalf("NamespacePackages() = %#v, want %#v", got, want)
	}
}

func TestDiscoverySourceExcludesAllSpiceGeneratedUnits(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		want bool
	}{
		{name: "application.go", want: true},
		{name: "application_test.go", want: false},
		{name: "spice_assembly_gen.go", want: false},
		{name: "spice_http_route_deadbeef_gen.go", want: false},
		{name: "application_spice_gen.go", want: false},
	}
	for _, test := range tests {
		if got := discoverySource(test.name); got != test.want {
			t.Errorf("discoverySource(%q) = %t, want %t", test.name, got, test.want)
		}
	}
}

func writeDiscoveryFile(
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
