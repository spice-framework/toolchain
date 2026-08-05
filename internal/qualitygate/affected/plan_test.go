package affected

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestSelectBuildsCrossModuleReverseClosure(t *testing.T) {
	t.Parallel()
	root := filepath.Clean(t.TempDir())
	consumer := filepath.Join(root, "examples", "consumer")
	graph := Graph{Packages: []Package{
		testPackage(root, "example.com/spice/config", "config"),
		{
			ImportPath: "example.com/spice/compiler",
			Directory:  filepath.Join(root, "compiler"),
			ModuleRoot: root,
			Imports:    []string{"example.com/spice/config"},
		},
		{
			ImportPath: "example.com/spice/examples/consumer/app",
			Directory:  filepath.Join(consumer, "app"),
			ModuleRoot: consumer,
			Imports:    []string{"example.com/spice/config"},
		},
	}}

	plan := Select(
		root,
		"base",
		[]string{"config/config.go"},
		graph,
	)
	if plan.Full {
		t.Fatalf("plan unexpectedly full: %+v", plan)
	}
	if len(plan.Modules) != 2 {
		t.Fatalf("modules = %+v, want two module plans", plan.Modules)
	}
	assertPackages(t, plan.Modules[0].Packages, []string{
		"example.com/spice/compiler",
		"example.com/spice/config",
	})
	assertPackages(t, plan.Modules[1].Packages, []string{
		"example.com/spice/examples/consumer/app",
	})
}

func TestSelectUsesTestImportsAndDeterministicOrder(t *testing.T) {
	t.Parallel()
	root := filepath.Clean(t.TempDir())
	graph := Graph{Packages: []Package{
		testPackage(root, "example.com/spice/alpha", "alpha"),
		{
			ImportPath:  "example.com/spice/zeta",
			Directory:   filepath.Join(root, "zeta"),
			ModuleRoot:  root,
			TestImports: []string{"example.com/spice/alpha"},
		},
	}}
	plan := Select(
		root,
		"base",
		[]string{"zeta/zeta_test.go", "alpha/alpha.go", "alpha/alpha.go"},
		graph,
	)
	if !slices.Equal(plan.GoFiles, []string{
		"alpha/alpha.go",
		"zeta/zeta_test.go",
	}) {
		t.Fatalf("GoFiles = %#v", plan.GoFiles)
	}
	assertPackages(t, plan.Modules[0].Packages, []string{
		"example.com/spice/alpha",
		"example.com/spice/zeta",
	})
}

func TestSelectWidensGlobalAndAmbiguousInputs(t *testing.T) {
	t.Parallel()
	root := filepath.Clean(t.TempDir())
	graph := Graph{Packages: []Package{
		testPackage(root, "example.com/spice/alpha", "alpha"),
		testPackage(root, "example.com/spice/beta", "beta"),
	}}
	for name, changed := range map[string][]string{
		"module":        {"go.mod"},
		"nested module": {"examples/consumer/go.mod"},
		"vendor":        {"vendor/modules.txt"},
		"nested vendor": {"examples/consumer/vendor/dependency/source.go"},
		"newpackage":    {"newpkg/new.go"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			plan := Select(root, "base", changed, graph)
			if !plan.Full {
				t.Fatalf("plan = %+v, want full", plan)
			}
			assertPackages(t, plan.Modules[0].Packages, []string{
				"example.com/spice/alpha",
				"example.com/spice/beta",
			})
			if strings.Contains(name, "vendor") && len(plan.GoFiles) != 0 {
				t.Fatalf("vendor plan GoFiles = %#v, want none", plan.GoFiles)
			}
		})
	}
}

func TestSelectClassifiesDocumentationEditorAndWorkspaceArtifacts(
	t *testing.T,
) {
	t.Parallel()
	root := filepath.Clean(t.TempDir())
	graph := Graph{Packages: []Package{
		testPackage(root, "example.com/spice/alpha", "alpha"),
	}}
	plan := Select(root, "base", []string{
		".tmp/private.txt",
		"docs/spring-coverage.md",
	}, graph)
	if len(plan.Modules) != 0 ||
		slices.Contains(plan.Changed, ".tmp/private.txt") ||
		!plan.SpringCoverage {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestChangedFilesIncludesCommittedStagedDirtyAndUntrackedPaths(
	t *testing.T,
) {
	t.Parallel()
	root := t.TempDir()
	runGit(t, root, "init", "-q")
	runGit(t, root, "config", "user.email", "spice@example.test")
	runGit(t, root, "config", "user.name", "Spice Test")
	writeFile(t, root, "committed.go", "package fixture\n")
	writeFile(t, root, "staged.go", "package fixture\n")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-q", "-m", "base")
	runGit(t, root, "update-ref", "refs/remotes/origin/main", "HEAD")

	writeFile(t, root, "committed.go", "package fixture\n\nvar changed = true\n")
	runGit(t, root, "add", "committed.go")
	runGit(t, root, "commit", "-q", "-m", "local")
	writeFile(t, root, "staged.go", "package fixture\n\nvar staged = true\n")
	runGit(t, root, "add", "staged.go")
	writeFile(t, root, "committed.go", "package fixture\n\nvar dirty = true\n")
	writeFile(t, root, "directory with spaces/untracked.go", "package untracked\n")

	base, paths, err := changedFiles(context.Background(), root, "")
	if err != nil {
		t.Fatalf("changedFiles() error = %v", err)
	}
	if strings.TrimSpace(base) == "" {
		t.Fatal("changedFiles() base is empty")
	}
	if !slices.Equal(paths, []string{
		"committed.go",
		"directory with spaces/untracked.go",
		"staged.go",
	}) {
		t.Fatalf("changedFiles() = %#v", paths)
	}
}

func TestBuildLoadsCrossModuleGoGraph(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/spice\n\ngo 1.26.0\n")
	writeFile(t, root, "alpha/alpha.go", "package alpha\n\nconst Value = 1\n")
	writeFile(
		t,
		root,
		"beta/beta.go",
		"package beta\n\nimport \"example.com/spice/alpha\"\n\nvar Value = alpha.Value\n",
	)
	writeFile(
		t,
		root,
		"examples/consumer/go.mod",
		"module example.com/spice/examples/consumer\n\n"+
			"go 1.26.0\n\n"+
			"require example.com/spice v0.0.0\n\n"+
			"replace example.com/spice => ../..\n",
	)
	writeFile(
		t,
		root,
		"examples/consumer/app/app.go",
		"package app\n\nimport \"example.com/spice/alpha\"\n\nvar Value = alpha.Value\n",
	)
	runGit(t, root, "init", "-q")
	runGit(t, root, "config", "user.email", "spice@example.test")
	runGit(t, root, "config", "user.name", "Spice Test")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-q", "-m", "base")
	runGit(t, root, "update-ref", "refs/remotes/origin/main", "HEAD")
	writeFile(
		t,
		root,
		"alpha/alpha.go",
		"package alpha\n\nconst Value = 2\n",
	)

	plan, err := Build(context.Background(), Config{
		RepositoryRoot: root,
		ModuleRoots:    []string{filepath.Join(root, "examples", "consumer")},
	})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if plan.Empty() || plan.Full || len(plan.Modules) != 2 {
		t.Fatalf("Build() = %+v", plan)
	}
	assertPackages(t, plan.Modules[0].Packages, []string{
		"example.com/spice/alpha",
		"example.com/spice/beta",
	})
	assertPackages(t, plan.Modules[1].Packages, []string{
		"example.com/spice/examples/consumer/app",
	})
}

func TestBuildIgnoresWorkspaceOnlyChanges(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/spice\n\ngo 1.26.0\n")
	writeFile(t, root, "app.go", "package spice\n")
	runGit(t, root, "init", "-q")
	runGit(t, root, "config", "user.email", "spice@example.test")
	runGit(t, root, "config", "user.name", "Spice Test")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-q", "-m", "base")
	runGit(t, root, "update-ref", "refs/remotes/origin/main", "HEAD")
	writeFile(t, root, ".tmp/private.txt", "private")

	plan, err := Build(context.Background(), Config{RepositoryRoot: root})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if !plan.Empty() {
		t.Fatalf("Build() = %+v, want empty", plan)
	}
}

func TestBuildAndGraphFailurePaths(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/spice\n\ngo 1.26.0\n")
	writeFile(t, root, "app.go", "package spice\n")
	runGit(t, root, "init", "-q")
	runGit(t, root, "config", "user.email", "spice@example.test")
	runGit(t, root, "config", "user.name", "Spice Test")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-q", "-m", "base")
	runGit(t, root, "update-ref", "refs/remotes/origin/main", "HEAD")

	if _, err := Build(
		context.Background(),
		Config{RepositoryRoot: root, Base: "missing-revision"},
	); err == nil || !strings.Contains(err.Error(), "missing-revision") {
		t.Fatalf("Build(missing base) error = %v", err)
	}
	if _, err := loadModule(context.Background(), filepath.Join(root, "missing")); err == nil {
		t.Fatal("loadModule(missing) error = nil")
	}
}

func TestFindModuleRootAndPlanEmpty(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/spice\n\ngo 1.26.0\n")
	nested := filepath.Join(root, "nested", "new.go")
	got, found := findModuleRoot(root, nested)
	if !found || filepath.Clean(got) != filepath.Clean(root) {
		t.Fatalf("findModuleRoot() = %q, %t", got, found)
	}
	if !(Plan{}).Empty() || (Plan{Changed: []string{"app.go"}}).Empty() {
		t.Fatal("Plan.Empty() returned an unexpected result")
	}
}

func TestNormalizePathsHandlesWindowsAndDuplicates(t *testing.T) {
	t.Parallel()
	got := normalizePaths([]string{
		`.\\compiler\\service\\service.go`,
		"compiler/service/service.go",
		"",
	})
	if !slices.Equal(got, []string{"compiler/service/service.go"}) {
		t.Fatalf("normalizePaths() = %#v", got)
	}
}

func TestSplitNULPreservesSpaces(t *testing.T) {
	t.Parallel()
	got := splitNUL([]byte("one.go\x00directory with spaces/two.go\x00"))
	if !slices.Equal(got, []string{
		"one.go",
		"directory with spaces/two.go",
	}) {
		t.Fatalf("splitNUL() = %#v", got)
	}
}

func testPackage(root, importPath, directory string) Package {
	return Package{
		ImportPath: importPath,
		Directory:  filepath.Join(root, directory),
		ModuleRoot: root,
	}
}

func assertPackages(t *testing.T, got, want []string) {
	t.Helper()
	if !slices.Equal(got, want) {
		t.Fatalf("packages = %#v, want %#v", got, want)
	}
}

func runGit(t *testing.T, root string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", arguments...)
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf(
			"git %s: %v\n%s",
			strings.Join(arguments, " "),
			err,
			output,
		)
	}
}

func writeFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
