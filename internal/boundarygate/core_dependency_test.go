package boundarygate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spice-framework/toolchain/internal/identity"
)

func TestCoreDependencyIdentityIsExactAcrossCandidateGraphs(t *testing.T) {
	t.Parallel()
	if identity.CoreVersion != "v0.1.0-preview.4" {
		t.Fatalf("core version = %q", identity.CoreVersion)
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if err = (verifier{root: root}).coreDependencyIdentity(); err != nil {
		t.Fatal(err)
	}
}

func TestCoreDependencyIdentityRejectsGraphDrift(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		mutate func(string)
	}{
		{name: "root stale", mutate: func(root string) {
			writeCoreDependencyModule(t, root, ".", "v0.1.0-preview.3")
		}},
		{name: "fixture missing", mutate: func(root string) {
			writeGateFile(t, root, "testdata/annotationfixture/go.mod", "module example.com/fixture\n\ngo 1.26.0\n")
		}},
		{name: "app duplicate", mutate: func(root string) {
			path := filepath.Join(root, "testdata", "annotationapp", "go.mod")
			content, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			content = append(content, []byte("\nrequire "+identity.CoreModule+" v0.1.0-preview.3\n")...)
			if err = os.WriteFile(path, content, 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "vendor stale", mutate: func(root string) {
			writeGateFile(t, root, "vendor/modules.txt", "# "+identity.CoreModule+" v0.1.0-preview.3\n")
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := coreDependencyFixture(t)
			test.mutate(root)
			if err := (verifier{root: root}).coreDependencyIdentity(); err == nil {
				t.Fatal("mutated core dependency identity succeeded")
			}
		})
	}
}

func coreDependencyFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, relative := range coreDependencyModules {
		writeCoreDependencyModule(t, root, relative, identity.CoreVersion)
	}
	writeGateFile(t, root, "vendor/modules.txt", "# "+identity.CoreModule+" "+identity.CoreVersion+"\n## explicit; go 1.26.0\n")
	return root
}

func writeCoreDependencyModule(t *testing.T, root, relative, version string) {
	t.Helper()
	name := "example.com/" + strings.ReplaceAll(strings.Trim(relative, "."), "/", "-")
	if relative == "." {
		name = identity.ToolchainModule
	}
	writeGateFile(
		t,
		root,
		filepath.ToSlash(filepath.Join(relative, "go.mod")),
		"module "+name+"\n\ngo 1.26.0\n\nrequire "+identity.CoreModule+" "+version+"\n",
	)
}
