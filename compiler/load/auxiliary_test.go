package load

import (
	"context"
	"go/types"
	"reflect"
	"strings"
	"testing"
)

func TestLoadAuxiliaryPackagesShareTypesButRemainNonPrimary(t *testing.T) {
	root := writeModule(t, map[string]string{
		"go.mod": "module example.com/auxiliary\n\ngo 1.26.0\n",
		"extension/extension.go": `package extension

type Client struct{}

func New() *Client {
	return &Client{}
}
`,
		"app/app.go": `package app

import "example.com/auxiliary/extension"

func Use(client *extension.Client) {}
`,
	})
	auxiliary := []string{
		"example.com/auxiliary/extension",
		"example.com/auxiliary/extension",
	}
	program, err := Load(
		context.Background(),
		Options{Dir: root, AuxiliaryPackages: auxiliary},
		"./app",
	)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if want := []string{
		"example.com/auxiliary/app",
		"example.com/auxiliary/extension",
	}; !reflect.DeepEqual(packagePaths(program.Packages()), want) {
		t.Fatalf("Packages() = %v, want %v", packagePaths(program.Packages()), want)
	}
	if want := []string{"example.com/auxiliary/app"}; !reflect.DeepEqual(
		packagePaths(program.PrimaryPackages()),
		want,
	) {
		t.Fatalf("PrimaryPackages() = %v, want %v", packagePaths(program.PrimaryPackages()), want)
	}
	packages := program.Packages()
	if len(packages) != 2 {
		t.Fatalf("Packages() count = %d", len(packages))
	}
	if packages[0].Auxiliary || !packages[1].Auxiliary {
		t.Fatalf("auxiliary flags = %#v", packages)
	}
	if symbolByID(program.Symbols(), "example.com/auxiliary/extension.New") == nil {
		t.Fatal("Symbols() excluded auxiliary constructor")
	}
	if symbolByID(program.PrimarySymbols(), "example.com/auxiliary/extension.New") != nil {
		t.Fatal("PrimarySymbols() included auxiliary constructor")
	}

	use := symbolByID(program.Symbols(), "example.com/auxiliary/app.Use")
	constructor := symbolByID(program.Symbols(), "example.com/auxiliary/extension.New")
	if use == nil || constructor == nil ||
		!types.Identical(
			use.Signature.Params().At(0).Type(),
			constructor.Signature.Results().At(0).Type(),
		) {
		t.Fatal("primary and auxiliary roots do not share one exact type universe")
	}
	if !reflect.DeepEqual(auxiliary, []string{
		"example.com/auxiliary/extension",
		"example.com/auxiliary/extension",
	}) {
		t.Fatalf("Load() mutated AuxiliaryPackages: %v", auxiliary)
	}
}

func TestLoadRejectsInvalidAuxiliaryPackagePatterns(t *testing.T) {
	for _, value := range []string{"", " ./extension", "./extension", "example.com/...", `example.com\extension`} {
		t.Run(strings.ReplaceAll(value, "/", "_"), func(t *testing.T) {
			program, err := Load(
				context.Background(),
				Options{AuxiliaryPackages: []string{value}},
				".",
			)
			if err == nil ||
				program == nil ||
				len(program.Diagnostics()) != 1 ||
				!strings.Contains(err.Error(), "one exact trimmed Go import path") {
				t.Fatalf("Load() program=%#v error=%v", program, err)
			}
		})
	}
}
