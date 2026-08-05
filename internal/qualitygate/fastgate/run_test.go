package fastgate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spice-framework/spice/internal/qualitygate/affected"
)

func TestRunNoAffectedWork(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	err := Run(context.Background(), Config{
		RepositoryRoot: t.TempDir(),
		Base:           "requested",
		Stdout:         &output,
		BuildPlan: func(
			_ context.Context,
			config affected.Config,
		) (affected.Plan, error) {
			if config.Base != "requested" {
				t.Fatalf("Base = %q, want requested", config.Base)
			}
			return affected.Plan{Base: "origin/main"}, nil
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := output.String(); !strings.Contains(got, "no affected work") {
		t.Fatalf("Run() output = %q", got)
	}
}

func TestRunDocumentationAndEditorInputs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(
		t,
		root,
		"docs/spring-coverage.md",
		"| Area | Spring capability | Spice direction | Status |\n"+
			"|---|---|---|---|\n"+
			"| Core | Beans | Compile-time DI | available |\n",
	)
	var output bytes.Buffer
	err := Run(context.Background(), Config{
		RepositoryRoot: root,
		Stdout:         &output,
		BuildPlan: func(
			context.Context,
			affected.Config,
		) (affected.Plan, error) {
			return affected.Plan{
				Base:           "base",
				Changed:        []string{"docs/spring-coverage.md"},
				Reasons:        []string{"global contract changed"},
				SpringCoverage: true,
				Zed:            true,
			}, nil
		},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	for _, fragment := range []string{
		"Spring coverage resolution",
		"generated target boundaries",
		"Zed inputs changed",
		"affected verification passed",
	} {
		if !strings.Contains(output.String(), fragment) {
			t.Errorf("Run() output does not contain %q:\n%s", fragment, output.String())
		}
	}
}

func TestRunReportsPlanningFailure(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("selection unavailable")
	err := Run(context.Background(), Config{
		RepositoryRoot: t.TempDir(),
		BuildPlan: func(
			context.Context,
			affected.Config,
		) (affected.Plan, error) {
			return affected.Plan{}, sentinel
		},
	})
	if err == nil {
		t.Fatal("Run() returned nil")
	}
	if !errors.Is(err, sentinel) ||
		!strings.Contains(err.Error(), "plan affected verification") {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestCheckGeneratedTargetBoundaries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		path      string
		content   string
		manifest  bool
		owned     bool
		wantError string
	}{
		{
			name:     "bounded source unit",
			path:     "internal/spicegen/app/spice_orders_gen.go",
			content:  "package app\n",
			manifest: true,
			owned:    true,
		},
		{
			name:      "retired monolith",
			path:      "internal/spicegen/app/zz_spice_gen.go",
			content:   "package app\n",
			manifest:  true,
			owned:     true,
			wantError: "retired generated target monolith",
		},
		{
			name:     "nested module source unit",
			path:     "examples/petclinic/internal/spicegen/app/spice_orders_gen.go",
			content:  "package app\n",
			manifest: true,
			owned:    true,
		},
		{
			name:      "oversized unit",
			path:      "testdata/annotationapp/internal/spicegen/app/spice_orders_gen.go",
			content:   strings.Repeat("// generated\n", maximumGeneratedTargetLines+1),
			manifest:  true,
			owned:     true,
			wantError: "must not exceed",
		},
		{
			name:      "handwritten source rejected",
			path:      "testdata/annotationapp/internal/spicegen/app/helpers.go",
			content:   "package app\n",
			manifest:  true,
			wantError: "is not owned",
		},
		{
			name:      "target without manifest rejected",
			path:      "internal/spicegen/app/spice_orders_gen.go",
			content:   "package app\n",
			wantError: "has no ownership manifest",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			moduleRoot, moduleFile := generatedTestModule(root, test.path)
			if test.manifest {
				files := []string{"internal/spicegen/app/spice_assembly_gen.go"}
				if test.owned {
					files = []string{moduleFile}
				} else {
					writeFile(t, moduleRoot, files[0], "package app\n")
				}
				writeGeneratedOwnershipManifest(t, moduleRoot, files)
			}
			writeFile(t, root, test.path, test.content)
			err := CheckGeneratedTargetBoundaries(root)
			if test.wantError == "" && err != nil {
				t.Fatalf("CheckGeneratedTargetBoundaries() error = %v", err)
			}
			if test.wantError != "" &&
				(err == nil || !strings.Contains(err.Error(), test.wantError)) {
				t.Fatalf(
					"CheckGeneratedTargetBoundaries() error = %v, want %q",
					err,
					test.wantError,
				)
			}
		})
	}
}

func TestCheckSpringCoverage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		content   string
		wantError string
	}{
		{
			name: "resolved",
			content: "| Area | Spring capability | Spice direction | Status |\n" +
				"|---|---|---|---|\n" +
				"| Core | Beans | Compile-time DI | available |\n" +
				"| Data | ORM | Deliberately explicit SQL | not-planned |\n",
		},
		{
			name:      "no rows",
			content:   "# Empty\n",
			wantError: "no capability rows",
		},
		{
			name: "invalid columns",
			content: "| Area | Spring capability | Spice direction | Status |\n" +
				"| Core | Beans | available |\n",
			wantError: "columns",
		},
		{
			name: "invalid status",
			content: "| Area | Spring capability | Spice direction | Status |\n" +
				"| Core | Beans | Later | pending |\n",
			wantError: "invalid status",
		},
		{
			name: "missing deliberate rationale",
			content: "| Area | Spring capability | Spice direction | Status |\n" +
				"| Data | ORM | Explicit SQL | not-planned |\n",
			wantError: "deliberate rationale",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeFile(t, root, "docs/spring-coverage.md", test.content)
			err := checkSpringCoverage(root)
			if test.wantError == "" && err != nil {
				t.Fatalf("checkSpringCoverage() error = %v", err)
			}
			if test.wantError != "" &&
				(err == nil || !strings.Contains(err.Error(), test.wantError)) {
				t.Fatalf("checkSpringCoverage() error = %v, want %q", err, test.wantError)
			}
		})
	}
}

func TestStepIncludesNameAndFailure(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	reporter := log.New(&output, "", 0)
	sentinel := errors.New("failed")
	err := step(reporter, "example", func() error { return sentinel })
	if err == nil {
		t.Fatal("step() returned nil")
	}
	if !errors.Is(err, sentinel) || !strings.Contains(err.Error(), "example") {
		t.Fatalf("step() error = %v", err)
	}
	if !strings.Contains(output.String(), "==> example") {
		t.Fatalf("step() output = %q", output.String())
	}
}

func TestFileLineCountRejectsMissingFile(t *testing.T) {
	t.Parallel()

	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatalf("OpenRoot() error = %v", err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	if _, err := fileLineCount(root, "missing.go"); err == nil {
		t.Fatal("fileLineCount() accepted a missing file")
	}
}

func TestRunModuleSkipsEmptySelection(t *testing.T) {
	t.Parallel()

	err := runModule(
		context.Background(),
		log.New(io.Discard, "", 0),
		t.TempDir(),
		affected.ModulePlan{},
	)
	if err != nil {
		t.Fatalf("runModule() error = %v", err)
	}
}

func TestRunModuleExecutesVetAndTests(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/affected\n\ngo 1.26.0\n")
	writeFile(t, root, "value.go", "package affected\n\nfunc Value() int { return 1 }\n")
	writeFile(
		t,
		root,
		"value_test.go",
		"package affected\n\nimport \"testing\"\n\n"+
			"func TestValue(t *testing.T) { if Value() != 1 { t.Fatal(\"wrong\") } }\n",
	)
	var output bytes.Buffer
	err := runModule(
		context.Background(),
		log.New(&output, "", 0),
		root,
		affected.ModulePlan{Root: root, Packages: []string{"./..."}},
	)
	if err != nil {
		t.Fatalf("runModule() error = %v", err)
	}
	if !strings.Contains(output.String(), "affected vet") ||
		!strings.Contains(output.String(), "affected test") {
		t.Fatalf("runModule() output = %q", output.String())
	}
}

func TestRunModuleReportsTestFailure(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/failing\n\ngo 1.26.0\n")
	writeFile(t, root, "value.go", "package failing\n")
	writeFile(
		t,
		root,
		"value_test.go",
		"package failing\n\nimport \"testing\"\n\n"+
			"func TestFailure(t *testing.T) { t.Fatal(\"expected\") }\n",
	)
	err := runModule(
		context.Background(),
		log.New(io.Discard, "", 0),
		root,
		affected.ModulePlan{Root: root, Packages: []string{"./..."}},
	)
	if err == nil || !strings.Contains(err.Error(), "affected test") {
		t.Fatalf("runModule() error = %v", err)
	}
}

func TestCheckFormatted(t *testing.T) {
	t.Parallel()

	root := repositoryRoot(t)
	err := checkFormatted(
		context.Background(),
		root,
		[]string{
			"internal/qualitygate/fastgate/run.go",
			"removed.go",
		},
	)
	if err != nil {
		t.Fatalf("checkFormatted() error = %v", err)
	}

	tempRoot := t.TempDir()
	err = os.Mkdir(filepath.Join(tempRoot, "directory.go"), 0o750)
	if err != nil {
		t.Fatalf("Mkdir() error = %v", err)
	}
	err = checkFormatted(
		context.Background(),
		tempRoot,
		[]string{"directory.go"},
	)
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("checkFormatted() directory error = %v", err)
	}
}

func TestToolPathRejectsUnknownTool(t *testing.T) {
	t.Parallel()

	_, err := toolPath(
		context.Background(),
		repositoryRoot(t),
		"spice-tool-that-does-not-exist",
	)
	if err == nil {
		t.Fatal("toolPath() accepted an unknown tool")
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	current, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(current, "tools", "go.mod")); err == nil {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			t.Fatal("repository root not found")
		}
		current = parent
	}
}

func generatedTestModule(root, file string) (string, string) {
	for _, prefix := range []string{
		"examples/petclinic/",
		"testdata/annotationapp/",
	} {
		if relative, found := strings.CutPrefix(file, prefix); found {
			return filepath.Join(root, filepath.FromSlash(strings.TrimSuffix(prefix, "/"))), relative
		}
	}
	return root, file
}

func writeGeneratedOwnershipManifest(
	t *testing.T,
	moduleRoot string,
	files []string,
) {
	t.Helper()
	manifestFiles := make([]map[string]string, 0, len(files))
	for _, file := range files {
		manifestFiles = append(manifestFiles, map[string]string{"path": file})
	}
	manifest := map[string]any{
		"target": map[string]string{"output_dir": "internal/spicegen/app"},
		"files":  manifestFiles,
	}
	content, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	writeFile(t, moduleRoot, ".spice/app.manifest.json", string(content))
}

func writeFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", name, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", name, err)
	}
}
