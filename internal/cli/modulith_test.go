package cli

import (
	"strings"
	"testing"
)

func TestRunVerifyEnforcesModuleImportBoundaries(t *testing.T) {
	t.Parallel()
	root := writeModule(t, map[string]string{
		"inventory/package.go": `// Package inventory owns inventory.
//
// @Module
package inventory
`,
		"inventory/storage/storage.go": `package storage

type Store struct{}
`,
		"orders/package.go": `// Package orders owns orders.
//
// @Module(allowedDependencies=["example.com/fixture/inventory"])
package orders
`,
		"orders/use/use.go": `package use

import "example.com/fixture/inventory/storage"

var Store storage.Store
`,
	})
	code, stdout, stderr := runModule(root, "verify", "./...")
	if code != 1 || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, expected := range []string{
		"imports internal package example.com/fixture/inventory/storage",
		"module architecture error(s)",
	} {
		if !strings.Contains(stderr, expected) {
			t.Fatalf("stderr=%q missing %q", stderr, expected)
		}
	}
}

func TestRunVerifyAcceptsDeclaredModuleRootAPI(t *testing.T) {
	t.Parallel()
	root := writeModule(t, map[string]string{
		"inventory/package.go": `// Package inventory owns inventory.
//
// @Module
package inventory

type Store struct{}
`,
		"orders/package.go": `// Package orders owns orders.
//
// @Module(allowedDependencies=["example.com/fixture/inventory"])
package orders
`,
		"orders/use/use.go": `package use

import "example.com/fixture/inventory"

var Store inventory.Store
`,
	})
	code, stdout, stderr := runModule(root, "verify", "./...")
	if code != 0 || !strings.Contains(stdout, "verification passed") || stderr != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}
