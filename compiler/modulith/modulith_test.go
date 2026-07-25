package modulith

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/StevenBuglione/spice/compiler/load"
	"github.com/StevenBuglione/spice/compiler/resolve"
)

func TestBuildDiscoversImportPathModulesAndLongestRootOwnership(t *testing.T) {
	root := writeModule(t, map[string]string{
		"cmd/shop/main.go": "package main\n\nfunc main() {}\n",
		"inventory/package.go": `// Package inventory owns stock.
//
// @Module
package inventory
`,
		"orders/package.go": `// Package orders owns order processing.
//
// @Module(allowedDependencies=["example.com/shop/inventory", "example.com/shop/payments::spi"])
package orders
`,
		"orders/admin/package.go": `// Package admin exposes administrative order operations.
//
// @NamedInterface("admin")
package admin
`,
		"orders/internal/storage/storage.go": "package storage\n",
		"orders/shipping/package.go": `// Package shipping is a nested module.
//
// @Module
package shipping
`,
		"orders/shipping/internal/label.go": "package internal\n",
		"payments/package.go": `// Package payments owns payment processing.
//
// @Module
package payments
`,
		"payments/spi/package.go": `// Package spi exposes payment integration.
//
// @NamedInterface(name="spi")
package spi
`,
		"shared/shared.go": "package shared\n",
	})

	model := buildModel(t, root)
	if diagnostics := model.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("Build() diagnostics = %v", diagnosticStrings(diagnostics))
	}
	modules := model.Modules()
	if got, want := moduleIDs(modules), []string{
		"example.com/shop/inventory",
		"example.com/shop/orders",
		"example.com/shop/orders/shipping",
		"example.com/shop/payments",
	}; !slices.Equal(got, want) {
		t.Fatalf("module IDs = %v, want %v", got, want)
	}

	orders := moduleByID(t, modules, "example.com/shop/orders")
	if got, want := packagePaths(orders.Packages()), []string{
		"example.com/shop/orders",
		"example.com/shop/orders/admin",
		"example.com/shop/orders/internal/storage",
	}; !slices.Equal(got, want) {
		t.Fatalf("orders packages = %v, want %v", got, want)
	}
	if packages := orders.Packages(); !packages[0].Root || packages[1].Root || packages[2].Root {
		t.Fatalf("orders root flags = %#v", packages)
	}
	if got := interfaceSummaries(orders.NamedInterfaces()); !slices.Equal(
		got,
		[]string{"admin=example.com/shop/orders/admin"},
	) {
		t.Fatalf("orders named interfaces = %v", got)
	}
	if got := dependencyStrings(orders.AllowedDependencies()); !slices.Equal(
		got,
		[]string{"example.com/shop/inventory", "example.com/shop/payments::spi"},
	) {
		t.Fatalf("orders allowed dependencies = %v", got)
	}

	shipping, ok := model.Owner("example.com/shop/orders/shipping/internal")
	if !ok || shipping.ID != "example.com/shop/orders/shipping" {
		t.Fatalf("shipping owner = %#v, %t", shipping, ok)
	}
	if _, ok := model.Owner("example.com/shop/shared"); ok {
		t.Fatal("unassigned shared package has an owner")
	}
	if got, want := packagePaths(model.UnassignedPackages()), []string{
		"example.com/shop/cmd/shop",
		"example.com/shop/shared",
	}; !slices.Equal(got, want) {
		t.Fatalf("unassigned packages = %v, want %v", got, want)
	}
}

func TestBuildReportsInvalidModuleMetadataDeterministically(t *testing.T) {
	files := map[string]string{
		"outside/package.go": `// Package outside is not assigned.
//
// @NamedInterface("orphan")
package outside
`,
		"orders/a_package.go": `// Package orders contains deliberately invalid metadata.
//
// @Module(allowedDependencies=["example.com/shop/orders", "example.com/shop/missing", "example.com/shop/payments::missing", "example.com/shop/payments::Bad", "example.com/shop/payments", "example.com/shop/payments", 7])
package orders
`,
		"orders/z_duplicate.go": `// @Module
package orders
`,
		"orders/first/package.go": `// Package first exposes a duplicate interface.
//
// @NamedInterface("admin")
package first
`,
		"orders/second/package.go": `// Package second exposes the same interface name.
//
// @NamedInterface("admin")
// @NamedInterface("Bad")
package second
`,
		"payments/package.go": `// Package payments is a module.
//
// @Module
package payments
`,
	}

	var first []string
	for iteration := range 10 {
		root := writeModule(t, files)
		model := buildModel(t, root)
		current := normalizedDiagnosticStrings(model.Diagnostics(), root)
		if iteration == 0 {
			first = current
		} else if !slices.Equal(current, first) {
			t.Fatalf("diagnostics changed: first=%v current=%v", first, current)
		}
	}
	for _, expected := range []string{
		"declares @Module more than once",
		"item 6 must be a string",
		"must not declare a dependency on itself",
		"allows unknown module example.com/shop/missing",
		"allows unknown interface missing",
		`named interface must match ^[a-z][a-z0-9-]*$`,
		"allowed dependency \"example.com/shop/payments\" is declared more than once",
		`declares named interface "admin" more than once`,
		`@NamedInterface name "Bad" must match`,
		`@NamedInterface "orphan" package example.com/shop/outside is not inside an @Module root`,
	} {
		if !containsDiagnostic(first, expected) {
			t.Fatalf("diagnostics %v do not contain %q", first, expected)
		}
	}
}

func TestBuildReturnsDefensiveCopiesAndRejectsInvalidInput(t *testing.T) {
	if diagnostics := Build(nil, resolve.Result{}).Diagnostics(); len(diagnostics) != 1 {
		t.Fatalf("Build(nil) diagnostics = %#v", diagnostics)
	}
	invalid := Build(&load.Program{}, resolve.Result{Diagnostics: []resolve.Diagnostic{{Message: "broken"}}})
	if diagnostics := invalid.Diagnostics(); len(diagnostics) != 1 || diagnostics[0].Kind != "resolution" {
		t.Fatalf("Build(resolution failure) diagnostics = %#v", diagnostics)
	}

	root := writeModule(t, map[string]string{
		"orders/package.go": `// Package orders is a module.
//
// @Module
package orders
`,
	})
	model := buildModel(t, root)
	modules := model.Modules()
	modules[0].packages[0].Path = "changed"
	if got := model.Modules()[0].Packages()[0].Path; got != "example.com/shop/orders" {
		t.Fatalf("Modules returned mutable storage: %q", got)
	}
	owner, ok := model.Owner("example.com/shop/orders")
	if !ok {
		t.Fatal("Owner(root) not found")
	}
	owner.packages[0].Path = "changed"
	if got, _ := model.Owner("example.com/shop/orders"); got.Packages()[0].Path != "example.com/shop/orders" {
		t.Fatal("Owner returned mutable storage")
	}
}

func TestBuildValidatesRootAndNamedInterfaceImportEdges(t *testing.T) {
	root := writeModule(t, map[string]string{
		"catalog/package.go": `// Package catalog exposes the product catalog.
//
// @Module
package catalog

type Product struct{}
`,
		"orders/package.go": `// Package orders owns orders.
//
// @Module(allowedDependencies=["example.com/shop/catalog", "example.com/shop/payments::spi"])
package orders
`,
		"orders/use/catalog.go": `package use

import "example.com/shop/catalog"

var Product catalog.Product
`,
		"orders/use/payments.go": `package use

import paymentspi "example.com/shop/payments/spi"

var Payment paymentspi.Request
`,
		"payments/package.go": `// Package payments owns payments.
//
// @Module
package payments
`,
		"payments/spi/package.go": `// Package spi exposes payment contracts.
//
// @NamedInterface("spi")
package spi

type Request struct{}
`,
	})
	model := buildModel(t, root)
	if diagnostics := model.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("Build() diagnostics = %v", diagnosticStrings(diagnostics))
	}
	if got, want := edgeSummaries(model.Edges()), []string{
		"example.com/shop/orders->example.com/shop/catalog:=true:true",
		"example.com/shop/orders->example.com/shop/payments:spi=true:true",
	}; !slices.Equal(got, want) {
		t.Fatalf("edges = %v, want %v", got, want)
	}
}

func TestBuildRejectsInternalAndUndeclaredImports(t *testing.T) {
	root := writeModule(t, map[string]string{
		"inventory/storage/storage.go": `package storage

type Store struct{}
`,
		"inventory/package.go": `// Package inventory owns inventory.
//
// @Module
package inventory
`,
		"orders/package.go": `// Package orders owns orders.
//
// @Module(allowedDependencies=["example.com/shop/inventory"])
package orders
`,
		"orders/use/use.go": `package use

import (
	"example.com/shop/inventory/storage"
	"example.com/shop/payments"
)

var (
	Store storage.Store
	Payment payments.Payment
)
`,
		"payments/package.go": `// Package payments owns payments.
//
// @Module
package payments

type Payment struct{}
`,
	})
	model := buildModel(t, root)
	diagnostics := normalizedDiagnosticStrings(model.Diagnostics(), root)
	for _, expected := range []string{
		"imports internal package example.com/shop/inventory/storage",
		"imports example.com/shop/payments through package example.com/shop/payments without declaring it",
	} {
		if !containsDiagnostic(diagnostics, expected) {
			t.Fatalf("diagnostics %v do not contain %q", diagnostics, expected)
		}
	}
	if got, want := edgeSummaries(model.Edges()), []string{
		"example.com/shop/orders->example.com/shop/inventory:=false:false",
		"example.com/shop/orders->example.com/shop/payments:=true:false",
	}; !slices.Equal(got, want) {
		t.Fatalf("edges = %v, want %v", got, want)
	}
}

func TestBuildDetectsDeterministicModuleCycles(t *testing.T) {
	root := writeModule(t, map[string]string{
		"a/package.go": `// Package a is a module.
//
// @Module(allowedDependencies=["example.com/shop/b"])
package a

type API struct{}
`,
		"a/use/first.go": `package use

import "example.com/shop/b"

var First b.API
`,
		"a/use/second.go": `package use

import "example.com/shop/b"

var Second b.API
`,
		"b/package.go": `// Package b is a module.
//
// @Module(allowedDependencies=["example.com/shop/c"])
package b

type API struct{}
`,
		"b/use/use.go": `package use

import "example.com/shop/c"

var C c.API
`,
		"c/package.go": `// Package c is a module.
//
// @Module(allowedDependencies=["example.com/shop/a"])
package c

type API struct{}
`,
		"c/use/use.go": `package use

import "example.com/shop/a"

var A a.API
`,
	})
	model := buildModel(t, root)
	if got := len(model.Edges()); got != 3 {
		t.Fatalf("edge count = %d, want 3 distinct package edges", got)
	}
	cycles := model.Cycles()
	if len(cycles) != 1 {
		t.Fatalf("Cycles() = %#v, want one", cycles)
	}
	wantMembers := []string{"example.com/shop/a", "example.com/shop/b", "example.com/shop/c"}
	wantPath := []string{
		"example.com/shop/a",
		"example.com/shop/b",
		"example.com/shop/c",
		"example.com/shop/a",
	}
	if !slices.Equal(cycles[0].Members, wantMembers) || !slices.Equal(cycles[0].Path, wantPath) {
		t.Fatalf("cycle = %#v, want members=%v path=%v", cycles[0], wantMembers, wantPath)
	}
	if diagnostics := model.Diagnostics(); len(diagnostics) != 1 ||
		!strings.Contains(diagnostics[0].Message, strings.Join(wantPath, " -> ")) {
		t.Fatalf("diagnostics = %v", diagnosticStrings(diagnostics))
	}

	cycles[0].Members[0] = "changed"
	cycles[0].Path[0] = "changed"
	if fresh := model.Cycles()[0]; fresh.Members[0] != wantMembers[0] || fresh.Path[0] != wantPath[0] {
		t.Fatal("Cycles returned mutable storage")
	}
	edges := model.Edges()
	edges[0].FromModule = "changed"
	if model.Edges()[0].FromModule == "changed" {
		t.Fatal("Edges returned mutable storage")
	}
	if _, err := model.Focus("example.com/shop/a"); err == nil ||
		!strings.Contains(err.Error(), "dependency cycle") {
		t.Fatalf("Focus(cyclic module) error = %v", err)
	}
}

func TestFocusIncludesOnlyObservedTransitiveDependencies(t *testing.T) {
	root := writeModule(t, map[string]string{
		"inventory/package.go": `// Package inventory owns inventory.
//
// @Module
package inventory

type Store struct{}
`,
		"orders/package.go": `// Package orders owns orders.
//
// @Module(allowedDependencies=["example.com/shop/inventory"])
package orders

type Order struct{}
`,
		"orders/use/use.go": `package use

import "example.com/shop/inventory"

var Store inventory.Store
`,
		"reporting/package.go": `// Package reporting owns reports.
//
// @Module(allowedDependencies=["example.com/shop/orders"])
package reporting
`,
		"reporting/use/use.go": `package use

import "example.com/shop/orders"

var Order orders.Order
`,
		"shipping/package.go": `// Package shipping owns shipping.
//
// @Module
package shipping
`,
		"shared/shared.go": "package shared\n",
	})
	model := buildModel(t, root)
	if diagnostics := model.Diagnostics(); len(diagnostics) != 0 {
		t.Fatalf("Build() diagnostics = %v", diagnosticStrings(diagnostics))
	}
	focused, err := model.Focus("example.com/shop/orders")
	if err != nil {
		t.Fatalf("Focus() error = %v", err)
	}
	if focused.FocusID() != "example.com/shop/orders" {
		t.Fatalf("FocusID() = %q", focused.FocusID())
	}
	if got, want := moduleIDs(focused.Modules()), []string{
		"example.com/shop/inventory",
		"example.com/shop/orders",
	}; !slices.Equal(got, want) {
		t.Fatalf("focused modules = %v, want %v", got, want)
	}
	if got, want := focused.DependencyOrder(), []string{
		"example.com/shop/inventory",
		"example.com/shop/orders",
	}; !slices.Equal(got, want) {
		t.Fatalf("dependency order = %v, want %v", got, want)
	}
	if len(focused.Edges()) != 1 || len(focused.UnassignedPackages()) != 0 {
		t.Fatalf(
			"focused edges/unassigned = %d/%d",
			len(focused.Edges()),
			len(focused.UnassignedPackages()),
		)
	}
	if owner, ok := focused.Owner("example.com/shop/orders/use"); !ok ||
		owner.ID != "example.com/shop/orders" {
		t.Fatalf("focused owner = %#v, %t", owner, ok)
	}
	order := focused.DependencyOrder()
	order[0] = "changed"
	if focused.DependencyOrder()[0] == "changed" {
		t.Fatal("DependencyOrder returned mutable storage")
	}
	if _, err := model.Focus("example.com/shop/missing"); err == nil {
		t.Fatal("Focus(unknown) error = nil")
	}
}

func buildModel(t *testing.T, root string) Model {
	t.Helper()
	program, err := load.Load(context.Background(), load.Options{Dir: root}, "./...")
	if err != nil {
		t.Fatalf("load.Load() error = %v", err)
	}
	resolution := resolve.Annotations(program)
	if len(resolution.Diagnostics) != 0 {
		t.Fatalf("resolve.Annotations() diagnostics = %v", resolution.Diagnostics)
	}
	return Build(program, resolution)
}

func writeModule(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/shop\n\ngo 1.26.0\n")
	for path, content := range files {
		writeFile(t, root, path, content)
	}
	return root
}

func writeFile(t *testing.T, root, relativePath, content string) {
	t.Helper()
	fullPath := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fullPath, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func moduleIDs(modules []Module) []string {
	result := make([]string, len(modules))
	for index, module := range modules {
		result[index] = module.ID
	}
	return result
}

func moduleByID(t *testing.T, modules []Module, id string) Module {
	t.Helper()
	for _, module := range modules {
		if module.ID == id {
			return module
		}
	}
	t.Fatalf("module %s not found in %v", id, moduleIDs(modules))
	return Module{}
}

func packagePaths(packages []Package) []string {
	result := make([]string, len(packages))
	for index, pkg := range packages {
		result[index] = pkg.Path
	}
	return result
}

func interfaceSummaries(interfaces []NamedInterface) []string {
	result := make([]string, len(interfaces))
	for index, item := range interfaces {
		result[index] = item.Name + "=" + item.PackagePath
	}
	return result
}

func dependencyStrings(dependencies []Dependency) []string {
	result := make([]string, len(dependencies))
	for index, dependency := range dependencies {
		result[index] = dependency.String()
	}
	return result
}

func edgeSummaries(edges []Edge) []string {
	result := make([]string, len(edges))
	for index, edge := range edges {
		result[index] = fmt.Sprintf(
			"%s->%s:%s=%t:%t",
			edge.FromModule,
			edge.ToModule,
			edge.API,
			edge.Exported,
			edge.Allowed,
		)
	}
	return result
}

func diagnosticStrings(diagnostics []Diagnostic) []string {
	result := make([]string, len(diagnostics))
	for index, diagnostic := range diagnostics {
		result[index] = diagnostic.Error()
	}
	return result
}

func normalizedDiagnosticStrings(diagnostics []Diagnostic, root string) []string {
	result := diagnosticStrings(diagnostics)
	for index := range result {
		result[index] = strings.ReplaceAll(result[index], filepath.Clean(root), "<root>")
	}
	return result
}

func containsDiagnostic(diagnostics []string, expected string) bool {
	for _, diagnostic := range diagnostics {
		if strings.Contains(diagnostic, expected) {
			return true
		}
	}
	return false
}

func ExampleDependency_String() {
	dependency := Dependency{
		ModuleID:  "example.com/shop/payments",
		Interface: "spi",
	}
	fmt.Println(dependency.String())
	// Output: example.com/shop/payments::spi
}
