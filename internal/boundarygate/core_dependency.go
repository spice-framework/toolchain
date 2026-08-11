package boundarygate

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spice-framework/toolchain/internal/identity"
	"golang.org/x/mod/modfile"
)

var coreDependencyModules = []string{
	".",
	"testdata/annotationfixture",
	"testdata/annotationapp",
}

func (gate verifier) coreDependencyIdentity() (returnErr error) {
	root, err := os.OpenRoot(gate.root)
	if err != nil {
		return fmt.Errorf("open core dependency root: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, root.Close())
	}()
	for _, relative := range coreDependencyModules {
		path := filepath.Join(filepath.FromSlash(relative), "go.mod")
		content, readErr := root.ReadFile(path)
		if readErr != nil {
			return fmt.Errorf("read %s core dependency identity: %w", relative, readErr)
		}
		parsed, parseErr := modfile.Parse(path, content, nil)
		if parseErr != nil {
			return fmt.Errorf("parse %s core dependency identity: %w", relative, parseErr)
		}
		var versions []string
		for _, requirement := range parsed.Require {
			if requirement.Mod.Path == identity.CoreModule {
				versions = append(versions, requirement.Mod.Version)
			}
		}
		if len(versions) != 1 || versions[0] != identity.CoreVersion {
			return fmt.Errorf(
				"%s must require exactly %s@%s, got %v",
				relative,
				identity.CoreModule,
				identity.CoreVersion,
				versions,
			)
		}
	}
	vendor, err := root.ReadFile(filepath.Join("vendor", "modules.txt"))
	if err != nil {
		return fmt.Errorf("read vendor core dependency identity: %w", err)
	}
	want := []byte("# " + identity.CoreModule + " " + identity.CoreVersion + "\n")
	if count := bytes.Count(vendor, want); count != 1 {
		return fmt.Errorf("vendor must select exact core dependency once, got %d", count)
	}
	return nil
}
