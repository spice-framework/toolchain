package modulith

import (
	"context"
	"slices"
	"testing"

	"github.com/spice-framework/spice/compiler/load"
	"github.com/spice-framework/spice/compiler/resolve"
	"github.com/spice-framework/spice/internal/testannotation"
)

func TestBuildExcludesAuxiliaryPackagesFromApplicationModules(t *testing.T) {
	root := writeModule(t, map[string]string{
		"extension/extension.go": `// Package extension is compiler support.
//
// @Module
package extension

type Client struct{}
`,
		"app/app.go": `// Package app is the application module.
//
// @Module
package app

import "example.com/shop/extension"

func Use(*extension.Client) {}
`,
	})
	program, err := load.Load(
		context.Background(),
		load.Options{
			Dir:               root,
			AuxiliaryPackages: []string{"example.com/shop/extension"},
		},
		"./app",
	)
	if err != nil {
		t.Fatalf("load.Load() error = %v", err)
	}
	resolution := resolve.Annotations(program)
	if len(resolution.Diagnostics) != 0 {
		t.Fatalf("resolve.Annotations() diagnostics = %v", resolution.Diagnostics)
	}
	resolution, err = testannotation.AttachOfficial(resolution)
	if err != nil {
		t.Fatalf("AttachOfficial() error = %v", err)
	}

	model := Build(program, resolution)
	if diagnostics := model.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("Build() diagnostics = %v", diagnosticStrings(diagnostics))
	}
	if got, want := moduleIDs(model.Modules()), []string{"example.com/shop/app"}; !slices.Equal(got, want) {
		t.Fatalf("Modules() = %v, want %v", got, want)
	}
	if unassigned := model.UnassignedPackages(); len(unassigned) != 0 {
		t.Fatalf("UnassignedPackages() = %#v", unassigned)
	}
	if edges := model.Edges(); len(edges) != 0 {
		t.Fatalf("Edges() = %#v", edges)
	}
}
