// Package testsupport owns portable module fixtures shared by toolchain tests.
package testsupport

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/spice-framework/toolchain/internal/identity"
)

type testingTB interface {
	Helper()
	Fatalf(string, ...any)
}

var (
	coreDirectoryOnce sync.Once
	coreDirectory     string
	coreDirectoryErr  error
)

// CoreDirectory returns the standard Go-selected source directory for the
// pinned public core dependency. Tests use it for temporary local replacements
// without committing machine-specific paths.
func CoreDirectory(t testingTB) string {
	t.Helper()
	coreDirectoryOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		// #nosec G204 -- executable, arguments, and pinned module identity are constants.
		command := exec.CommandContext(
			ctx,
			"go",
			"list",
			"-mod=mod",
			"-m",
			"-f={{.Dir}}",
			identity.CoreModule,
		)
		output, err := command.CombinedOutput()
		if err != nil {
			coreDirectoryErr = err
			return
		}
		coreDirectory = filepath.Clean(strings.TrimSpace(string(output)))
	})
	if coreDirectoryErr != nil {
		t.Fatalf("resolve pinned Spice core module: %v", coreDirectoryErr)
	}
	if coreDirectory == "" || coreDirectory == "." {
		t.Fatalf("resolve pinned Spice core module: empty directory")
	}
	return coreDirectory
}
