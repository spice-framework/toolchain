package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	codegen "github.com/StevenBuglione/spice/compiler/generate"
	"github.com/StevenBuglione/spice/internal/devloop"
)

func TestDevelopmentPipelineReusesBodyOnlyGeneration(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source := "package app\n\n// @Service\nfunc NewService() string { return \"one\" }\n"
	writeIncrementalFile(t, root, "service.go", source)
	structures, err := structuralSnapshot(context.Background(), root)
	if err != nil {
		t.Fatalf("structuralSnapshot() error = %v", err)
	}
	pipeline := &developmentPipeline{
		root:               root,
		cachedTargetName:   "Application",
		cachedStructures:   structures,
		hasGenerationCache: true,
	}
	writeIncrementalFile(
		t,
		root,
		"service.go",
		"package app\n\n// @Service\nfunc NewService() string { return \"two\" }\n",
	)
	_, target, reused, err := pipeline.reusableGeneration(
		developmentBatch(t, "service.go", devloop.ChangeWrite),
	)
	if err != nil {
		t.Fatalf("reusableGeneration() error = %v", err)
	}
	if !reused || target != "Application" {
		t.Fatalf("reusableGeneration() = target %q, reused %t", target, reused)
	}
}

func TestDevelopmentPipelineRejectsStructuralReuse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		path    string
		kind    devloop.ChangeKind
		content string
	}{
		{
			name:    "signature changed",
			path:    "service.go",
			kind:    devloop.ChangeWrite,
			content: "package app\n\n// @Service\nfunc NewService(name string) string { return name }\n",
		},
		{
			name:    "non-Go input",
			path:    "application.yaml",
			kind:    devloop.ChangeWrite,
			content: "server.port: 8081\n",
		},
		{
			name:    "created source",
			path:    "created.go",
			kind:    devloop.ChangeCreate,
			content: "package app\n",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeIncrementalFile(
				t,
				root,
				"service.go",
				"package app\n\n// @Service\nfunc NewService() string { return \"one\" }\n",
			)
			structures, err := structuralSnapshot(context.Background(), root)
			if err != nil {
				t.Fatalf("structuralSnapshot() error = %v", err)
			}
			pipeline := &developmentPipeline{
				root:               root,
				cachedTargetName:   "Application",
				cachedStructures:   structures,
				hasGenerationCache: true,
			}
			writeIncrementalFile(t, root, test.path, test.content)
			_, _, reused, err := pipeline.reusableGeneration(
				developmentBatch(t, test.path, test.kind),
			)
			if err != nil {
				t.Fatalf("reusableGeneration() error = %v", err)
			}
			if reused {
				t.Fatal("reusableGeneration() reused a structural change")
			}
		})
	}
}

func TestDevelopmentPipelineGenerationCacheLifecycle(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeIncrementalFile(t, root, "app.go", "package app\n")
	writeIncrementalFile(t, root, "testdata/invalid.go", "not go")
	writeIncrementalFile(
		t,
		root,
		"internal/spicegen/app/generated.go",
		"not go",
	)
	pipeline := &developmentPipeline{root: root}
	_, _, reused, err := pipeline.reusableGeneration(
		developmentBatch(t, "app.go", devloop.ChangeWrite),
	)
	if err != nil || reused {
		t.Fatalf("empty reusableGeneration() = reused %t, error %v", reused, err)
	}
	if err := pipeline.rememberGeneration(
		context.Background(),
		codegen.Plan{},
		"Application",
	); err != nil {
		t.Fatalf("rememberGeneration() error = %v", err)
	}
	if !pipeline.hasGenerationCache ||
		pipeline.cachedTargetName != "Application" ||
		len(pipeline.cachedStructures) != 1 {
		t.Fatalf("rememberGeneration() cache = %+v", pipeline.cachedStructures)
	}
}

func developmentBatch(
	t *testing.T,
	path string,
	kind devloop.ChangeKind,
) devloop.Batch {
	t.Helper()
	debouncer, err := devloop.NewDebouncer(time.Nanosecond, time.Nanosecond)
	if err != nil {
		t.Fatalf("NewDebouncer() error = %v", err)
	}
	now := time.Now()
	event, err := devloop.NewFileEvent(path, kind)
	if err != nil {
		t.Fatalf("NewFileEvent() error = %v", err)
	}
	if err := debouncer.Add(now, event); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	batch, ready := debouncer.Take(now.Add(time.Nanosecond), 1)
	if !ready {
		t.Fatal("Take() did not produce a development batch")
	}
	return batch
}

func writeIncrementalFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", name, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", name, err)
	}
}
