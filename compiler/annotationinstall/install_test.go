package annotationinstall

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fixtureTool = "example.com/spice-annotation-fixture/cmd/spice-annotations"

func TestPreviewAndApplyDependencyUsesStandardGoGet(t *testing.T) {
	t.Parallel()
	root := writeInstallFixture(t)
	dependency := "github.com/StevenBuglione/spice/bean"
	preview, err := PreviewDependency(
		t.Context(),
		root,
		dependency,
		"v0.0.0",
		append(os.Environ(), "GOPROXY=off"),
	)
	if err != nil {
		t.Fatalf("PreviewDependency() error = %v", err)
	}
	if preview.Command() != "go get "+dependency+"@v0.0.0" ||
		preview.Dependency() != dependency ||
		preview.Tool() != "" ||
		!strings.Contains(preview.Diff(), "github.com/StevenBuglione/spice v0.0.0") {
		t.Fatalf(
			"PreviewDependency() = command %q, dependency %q, diff:\n%s",
			preview.Command(),
			preview.Dependency(),
			preview.Diff(),
		)
	}
	if err := Apply(t.Context(), preview); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if content := readInstallGoMod(t, root); !strings.Contains(
		content,
		"github.com/StevenBuglione/spice v0.0.0",
	) {
		t.Fatalf("applied go.mod:\n%s", content)
	}
	assertNoInstallTemporaryFiles(t, root)
}

func TestPreviewAndApplyToolUsesTemporaryModfileAndHashGuard(
	t *testing.T,
) {
	t.Parallel()
	root := writeInstallFixture(t)
	original := readInstallGoMod(t, root)
	preview, err := PreviewTool(
		context.Background(),
		root,
		fixtureTool,
		"v0.0.0",
		append(os.Environ(), "GOPROXY=off", "GOFLAGS=-mod=vendor"),
	)
	if err != nil {
		t.Fatalf("PreviewTool() error = %v", err)
	}
	if preview.Command() != "go get -tool "+fixtureTool+"@v0.0.0" ||
		preview.Root() != filepath.Clean(root) ||
		preview.Tool() != fixtureTool ||
		preview.Version() != "v0.0.0" ||
		preview.Token() == "" ||
		!strings.Contains(preview.Diff(), "--- go.mod") ||
		!strings.Contains(preview.Diff(), "+tool "+fixtureTool) {
		t.Fatalf("PreviewTool() = command %q, diff:\n%s", preview.Command(), preview.Diff())
	}
	if current := readInstallGoMod(t, root); current != original {
		t.Fatalf("preview changed go.mod:\n%s", current)
	}
	assertNoInstallTemporaryFiles(t, root)
	if err := Apply(context.Background(), preview); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	applied := readInstallGoMod(t, root)
	if !strings.Contains(applied, "tool "+fixtureTool) {
		t.Fatalf("applied go.mod:\n%s", applied)
	}
	assertNoInstallTemporaryFiles(t, root)
}

func TestApplyGuardsUnchangedGoSumAndRejectsInvalidPlans(t *testing.T) {
	t.Parallel()
	root := writeInstallFixture(t)
	preview, err := PreviewTool(
		context.Background(),
		root,
		fixtureTool,
		"v0.0.0",
		append(os.Environ(), "GOPROXY=off"),
	)
	if err != nil {
		t.Fatalf("PreviewTool() error = %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(root, "go.sum"),
		[]byte("developer checksum\n"),
		0o600,
	); err != nil {
		t.Fatalf("WriteFile(go.sum) error = %v", err)
	}
	if err := Apply(context.Background(), preview); !errors.Is(
		err,
		ErrStalePreview,
	) {
		t.Fatalf("Apply(stale go.sum) error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for name, applyErr := range map[string]error{
		"nil-context": Apply(
			nil, //nolint:staticcheck // The public fail-closed context contract is under test.
			preview,
		),
		"canceled": Apply(ctx, preview),
		"invalid":  Apply(context.Background(), Preview{}),
	} {
		if applyErr == nil {
			t.Fatalf("%s Apply() error = nil", name)
		}
	}
}

func TestPreviewReportsGoCommandFailureAndNoChange(t *testing.T) {
	t.Parallel()
	root := writeInstallFixture(t)
	_, err := PreviewTool(
		context.Background(),
		root,
		"example.invalid/not-cached/cmd/annotations",
		"v1.0.0",
		append(os.Environ(), "GOPROXY=off"),
	)
	if err == nil ||
		!strings.Contains(err.Error(), "go get -tool") {
		t.Fatalf("PreviewTool(missing) error = %v", err)
	}
	assertNoInstallTemporaryFiles(t, root)

	preview, err := PreviewTool(
		context.Background(),
		root,
		fixtureTool,
		"v0.0.0",
		append(os.Environ(), "GOPROXY=off"),
	)
	if err != nil {
		t.Fatalf("PreviewTool() error = %v", err)
	}
	if err := Apply(context.Background(), preview); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if _, err := PreviewTool(
		context.Background(),
		root,
		fixtureTool,
		"v0.0.0",
		append(os.Environ(), "GOPROXY=off"),
	); err == nil || !strings.Contains(err.Error(), "no module-file changes") {
		t.Fatalf("PreviewTool(already installed) error = %v", err)
	}
	assertNoInstallTemporaryFiles(t, root)
}

func TestApplyRollsBackStagedModuleFiles(t *testing.T) {
	t.Parallel()
	t.Run("backup failure", func(t *testing.T) {
		t.Parallel()
		rootPath := t.TempDir()
		original := []byte("module example.com/app\n")
		if err := os.WriteFile(
			filepath.Join(rootPath, "go.mod"),
			original,
			0o600,
		); err != nil {
			t.Fatalf("WriteFile(go.mod) error = %v", err)
		}
		root, err := os.OpenRoot(rootPath)
		if err != nil {
			t.Fatalf("OpenRoot() error = %v", err)
		}
		changes := []fileChange{
			{
				name:   "go.mod",
				before: newFileState(true, original),
				after:  newFileState(true, []byte("changed\n")),
				mode:   0o600,
			},
			{
				name:   "go.sum",
				before: newFileState(true, []byte("missing\n")),
				after:  newFileState(true, []byte("new\n")),
				mode:   0o600,
			},
		}
		applyErr := applyChanges(context.Background(), root, changes)
		closeErr := root.Close()
		if applyErr == nil || closeErr != nil {
			t.Fatalf(
				"applyChanges() error = %v, Close() error = %v",
				applyErr,
				closeErr,
			)
		}
		if current := readInstallGoMod(t, rootPath); current != string(original) {
			t.Fatalf("rollback go.mod = %q", current)
		}
		assertNoInstallTemporaryFiles(t, rootPath)
	})
	t.Run("stage failure", func(t *testing.T) {
		t.Parallel()
		rootPath := t.TempDir()
		root, err := os.OpenRoot(rootPath)
		if err != nil {
			t.Fatalf("OpenRoot() error = %v", err)
		}
		changes := []fileChange{
			{
				name:  "go.mod",
				after: newFileState(true, []byte("module example.com/app\n")),
				mode:  0o600,
			},
			{
				name:  "missing/go.sum",
				after: newFileState(true, []byte("sum\n")),
				mode:  0o600,
			},
		}
		applyErr := applyChanges(context.Background(), root, changes)
		closeErr := root.Close()
		if applyErr == nil || closeErr != nil {
			t.Fatalf(
				"applyChanges() error = %v, Close() error = %v",
				applyErr,
				closeErr,
			)
		}
		assertNoInstallTemporaryFiles(t, rootPath)
	})
}

func TestBoundedOutputDiffAndWriteBoundaries(t *testing.T) {
	t.Parallel()
	output := newBoundedBuffer(4)
	if written, err := output.Write([]byte("abcdef")); err != nil ||
		written != 6 ||
		output.String() != "abcd" ||
		!output.overflow {
		t.Fatalf(
			"bounded output = %d, %v, %q, overflow=%t",
			written,
			err,
			output.String(),
			output.overflow,
		)
	}
	if detail := commandFailureDetail(
		newBoundedBuffer(1),
		newBoundedBuffer(1),
	); detail != "" {
		t.Fatalf("empty commandFailureDetail() = %q", detail)
	}
	if detail := commandFailureDetail(output, output); detail == "" {
		t.Fatal("commandFailureDetail(output) is empty")
	}
	diff := unifiedFileDiff(fileChange{
		name:   "go.sum",
		before: newFileState(true, []byte("one\ntwo")),
		after:  newFileState(false, nil),
	})
	if !strings.Contains(diff, "+++ /dev/null") ||
		!strings.Contains(diff, "\\ No newline at end of file") ||
		unifiedRangeStart(4, 0) != 4 ||
		unifiedRangeStart(4, 1) != 5 {
		t.Fatalf("unified diff:\n%s", diff)
	}
	if moduleAuthorizesTool([]byte("broken"), fixtureTool) {
		t.Fatal("moduleAuthorizesTool(broken) = true")
	}
	if err := writeAll(zeroWriter{}, []byte("content")); !errors.Is(
		err,
		io.ErrShortWrite,
	) {
		t.Fatalf("writeAll(zero writer) error = %v", err)
	}
}

type zeroWriter struct{}

func (zeroWriter) Write([]byte) (int, error) {
	return 0, nil
}

func TestApplyRejectsChangedModuleFiles(t *testing.T) {
	t.Parallel()
	root := writeInstallFixture(t)
	preview, err := PreviewTool(
		context.Background(),
		root,
		fixtureTool,
		"v0.0.0",
		append(os.Environ(), "GOPROXY=off"),
	)
	if err != nil {
		t.Fatalf("PreviewTool() error = %v", err)
	}
	path := filepath.Join(root, "go.mod")
	changed := readInstallGoMod(t, root) + "\n// developer edit\n"
	if err := os.WriteFile(path, []byte(changed), 0o600); err != nil {
		t.Fatalf("WriteFile(go.mod) error = %v", err)
	}
	if err := Apply(context.Background(), preview); !errors.Is(
		err,
		ErrStalePreview,
	) {
		t.Fatalf("Apply(stale) error = %v", err)
	}
	if current := readInstallGoMod(t, root); current != changed {
		t.Fatalf("stale apply changed go.mod:\n%s", current)
	}
	assertNoInstallTemporaryFiles(t, root)
}

func TestPreviewToolRejectsInvalidInputsAndCancellation(t *testing.T) {
	t.Parallel()
	root := writeInstallFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	environment := append(os.Environ(), "GOPROXY=off")
	for name, err := range map[string]error{
		"nil-context": previewError(
			nil, //nolint:staticcheck // The public fail-closed context contract is under test.
			root,
			fixtureTool,
			"v0.0.0",
			environment,
		),
		"canceled": previewError(
			ctx,
			root,
			fixtureTool,
			"v0.0.0",
			environment,
		),
		"invalid-tool": previewError(
			context.Background(),
			root,
			"../tool",
			"v0.0.0",
			environment,
		),
		"invalid-version": previewError(
			context.Background(),
			root,
			fixtureTool,
			"latest",
			environment,
		),
	} {
		if err == nil {
			t.Fatalf("%s PreviewTool() error = nil", name)
		}
	}
	assertNoInstallTemporaryFiles(t, root)
}

func writeInstallFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	fixture := filepath.Join(repository, "testdata", "annotationfixture")
	content := "module example.com/install-app\n\ngo 1.26.0\n\n" +
		"require example.com/spice-annotation-fixture v0.0.0\n\n" +
		"replace example.com/spice-annotation-fixture => " +
		filepath.ToSlash(fixture) + "\n\n" +
		"replace github.com/StevenBuglione/spice => " +
		filepath.ToSlash(repository) + "\n"
	if err := os.WriteFile(
		filepath.Join(root, "go.mod"),
		[]byte(content),
		0o600,
	); err != nil {
		t.Fatalf("WriteFile(go.mod) error = %v", err)
	}
	return root
}

func previewError(
	ctx context.Context,
	root string,
	tool string,
	version string,
	environment []string,
) error {
	_, err := PreviewTool(ctx, root, tool, version, environment)
	return err
}

func readInstallGoMod(t *testing.T, root string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("ReadFile(go.mod) error = %v", err)
	}
	return string(content)
}

func assertNoInstallTemporaryFiles(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".spice-") {
			t.Fatalf("temporary file remains: %s", entry.Name())
		}
	}
}
