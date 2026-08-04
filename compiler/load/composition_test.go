package load

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"golang.org/x/tools/go/packages"
)

func TestLoadPromotesLocalBlankImportsIntoPrimaryComposition(t *testing.T) {
	const modulePath = "example.com/composition"
	dir := writeModule(t, map[string]string{
		"go.mod": "module " + modulePath + "\n\ngo 1.26.0\n",
		"cmd/app/main.go": `package main

import (
	_ "example.com/composition/orders"
	_ "example.com/composition/storage"
	"example.com/composition/shared"
)

var _ = shared.Value
func main() {}
`,
		"orders/orders.go": `package orders

import _ "example.com/composition/storage"

type OrderService struct{}
`,
		"shared/shared.go":   "package shared\n\nconst Value = 1\n",
		"storage/storage.go": "package storage\n",
	})

	program, err := Load(
		context.Background(),
		Options{Dir: dir},
		modulePath+"/cmd/app",
	)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	want := []string{
		modulePath + "/cmd/app",
		modulePath + "/orders",
		modulePath + "/storage",
	}
	if got := packagePaths(program.PrimaryPackages()); !reflect.DeepEqual(got, want) {
		t.Fatalf("PrimaryPackages() = %v, want %v", got, want)
	}
	if symbolByID(program.PrimarySymbols(), modulePath+"/orders.OrderService") == nil {
		t.Fatalf(
			"PrimarySymbols() excluded imported package declarations: %v",
			symbolIDs(program.PrimarySymbols()),
		)
	}
}

func TestLoadDoesNotPromoteExternalOrAutoconfigureBlankImports(t *testing.T) {
	const modulePath = "example.com/composition"
	dir := writeModule(t, map[string]string{
		"go.mod": "module " + modulePath + "\n\ngo 1.26.0\n",
		"cmd/app/main.go": `package main

import (
	_ "example.com/composition/library/autoconfigure"
	_ "net/http/pprof"
)

func main() {}
`,
		"library/autoconfigure/configuration.go": "package autoconfigure\n",
	})

	program, err := Load(
		context.Background(),
		Options{Dir: dir},
		modulePath+"/cmd/app",
	)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	want := []string{modulePath + "/cmd/app"}
	if got := packagePaths(program.PrimaryPackages()); !reflect.DeepEqual(got, want) {
		t.Fatalf("PrimaryPackages() = %v, want %v", got, want)
	}
}

func TestLoadCompositionUsesOverlayImports(t *testing.T) {
	const modulePath = "example.com/composition"
	dir := writeModule(t, map[string]string{
		"go.mod": "module " + modulePath + "\n\ngo 1.26.0\n",
		"cmd/app/main.go": `package main

import _ "example.com/composition/orders"

func main() {}
`,
		"orders/orders.go":   "package orders\n",
		"storage/storage.go": "package storage\n",
	})
	overlay := []byte(`package main

import _ "example.com/composition/storage"

func main() {}
`)

	program, err := Load(
		context.Background(),
		Options{
			Dir: dir,
			Overlay: map[string][]byte{
				filepath.Join(dir, "cmd", "app", "main.go"): overlay,
			},
		},
		"./cmd/app",
	)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	want := []string{modulePath + "/cmd/app", modulePath + "/storage"}
	if got := packagePaths(program.PrimaryPackages()); !reflect.DeepEqual(got, want) {
		t.Fatalf("PrimaryPackages() = %v, want overlay-selected %v", got, want)
	}
}

func TestLoadCompositionHonorsBuildTags(t *testing.T) {
	const modulePath = "example.com/composition"
	dir := writeModule(t, map[string]string{
		"go.mod": "module " + modulePath + "\n\ngo 1.26.0\n",
		"cmd/app/main.go": `package main

func main() {}
`,
		"cmd/app/tagged.go": `//go:build composition_extra

package main

import _ "example.com/composition/extra"
`,
		"extra/extra.go": "package extra\n",
	})

	defaultProgram, err := Load(
		context.Background(),
		Options{Dir: dir},
		"./cmd/app",
	)
	if err != nil {
		t.Fatalf("default Load() error = %v", err)
	}
	if got := packagePaths(defaultProgram.PrimaryPackages()); !reflect.DeepEqual(
		got,
		[]string{modulePath + "/cmd/app"},
	) {
		t.Fatalf("default PrimaryPackages() = %v", got)
	}

	taggedProgram, err := Load(
		context.Background(),
		Options{Dir: dir, BuildFlags: []string{"-tags=composition_extra"}},
		"./cmd/app",
	)
	if err != nil {
		t.Fatalf("tagged Load() error = %v", err)
	}
	want := []string{modulePath + "/cmd/app", modulePath + "/extra"}
	if got := packagePaths(taggedProgram.PrimaryPackages()); !reflect.DeepEqual(got, want) {
		t.Fatalf("tagged PrimaryPackages() = %v, want %v", got, want)
	}
}

func TestSelectProgramRootsRejectsNestedExternalModuleCandidate(t *testing.T) {
	const (
		modulePath         = "example.com/composition"
		applicationPath    = modulePath + "/cmd/app"
		externalModulePath = modulePath + "/plugin"
		externalPath       = externalModulePath + "/feature"
	)
	root := &packages.Package{
		ID:      applicationPath,
		PkgPath: applicationPath,
		Module:  &packages.Module{Path: modulePath},
	}
	external := &packages.Package{
		ID:      externalPath,
		PkgPath: externalPath,
		Module:  &packages.Module{Path: externalModulePath},
	}

	got := selectProgramRoots(
		[]*packages.Package{nil, root, external},
		map[string]struct{}{applicationPath: {}},
		nil,
		[]compositionCandidate{{
			importPath:  externalPath,
			ownerModule: modulePath,
		}},
	)
	if len(got) != 1 || got[0] != root {
		t.Fatalf("selectProgramRoots() = %v, want only the application root", got)
	}
}
