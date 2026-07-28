package service

import (
	"go/types"
	"testing"

	"golang.org/x/tools/go/packages"
)

func TestCollectTypedPackageVisitsSharedImportsOnce(t *testing.T) {
	t.Parallel()

	shared := testTypedPackage("example.com/shared")
	left := testTypedPackage("example.com/left")
	right := testTypedPackage("example.com/right")
	root := testTypedPackage("example.com/root")
	root.Imports = map[string]*packages.Package{
		left.PkgPath:  left,
		right.PkgPath: right,
	}
	left.Imports = map[string]*packages.Package{shared.PkgPath: shared}
	right.Imports = map[string]*packages.Package{shared.PkgPath: shared}
	// A malformed loader graph must not make editor metadata traversal recurse
	// forever. Real Go import graphs are acyclic, but this edge makes the
	// visited-package invariant directly observable.
	shared.Imports = map[string]*packages.Package{left.PkgPath: left}

	result := make(map[string]typedPackage)
	collectTypedPackage(result, root)

	if len(result) != 4 {
		t.Fatalf("collected package count = %d, want 4", len(result))
	}
	for _, packagePath := range []string{
		root.PkgPath,
		left.PkgPath,
		right.PkgPath,
		shared.PkgPath,
	} {
		if _, found := result[packagePath]; !found {
			t.Errorf("collected packages omit %q", packagePath)
		}
	}
}

func testTypedPackage(packagePath string) *packages.Package {
	name := packagePath[len("example.com/"):]
	return &packages.Package{
		Name:    name,
		PkgPath: packagePath,
		Types:   types.NewPackage(packagePath, name),
	}
}
