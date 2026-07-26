package devloop

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPollingWatcherDetectsContentAndRecursiveChanges(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writePollingFile(t, root, "main.go", "package main\n")
	clock := newFakeClock(time.Unix(50_000, 0))
	watcher, err := NewPollingWatcher(
		context.Background(),
		PollingConfig{
			Root:     root,
			Interval: time.Second,
			Maximum:  20,
		},
		clock,
	)
	if err != nil {
		t.Fatalf("NewPollingWatcher() error = %v", err)
	}
	t.Cleanup(func() {
		if closeErr := watcher.Close(); closeErr != nil {
			t.Errorf("Close() error = %v", closeErr)
		}
	})

	mainPath := filepath.Join(root, "main.go")
	mainInfo, err := os.Stat(mainPath)
	if err != nil {
		t.Fatalf("Stat(main.go) error = %v", err)
	}
	writePollingFile(t, root, "main.go", "package test\n")
	if err := os.Chtimes(
		mainPath,
		mainInfo.ModTime(),
		mainInfo.ModTime(),
	); err != nil {
		t.Fatalf("Chtimes(main.go) error = %v", err)
	}
	clock.Advance(time.Second)
	assertWatchEvent(t, watcher.Events(), FileEvent{
		Path: "main.go",
		Kind: ChangeWrite,
	})

	writePollingFile(t, root, "orders/service.go", "package orders\n")
	clock.Advance(time.Second)
	assertWatchEvent(t, watcher.Events(), FileEvent{
		Path: "orders/service.go",
		Kind: ChangeCreate,
	})

	if err := os.Remove(filepath.Join(root, "orders", "service.go")); err != nil {
		t.Fatalf("Remove(service.go) error = %v", err)
	}
	clock.Advance(time.Second)
	assertWatchEvent(t, watcher.Events(), FileEvent{
		Path: "orders/service.go",
		Kind: ChangeRemove,
	})
}

func TestPollingWatcherIgnoresGeneratedAndTemporaryWrites(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writePollingFile(t, root, "main.go", "package main\n")
	clock := newFakeClock(time.Unix(60_000, 0))
	watcher, err := NewPollingWatcher(
		context.Background(),
		PollingConfig{Root: root, Interval: time.Second},
		clock,
	)
	if err != nil {
		t.Fatalf("NewPollingWatcher() error = %v", err)
	}
	t.Cleanup(func() {
		if closeErr := watcher.Close(); closeErr != nil {
			t.Errorf("Close() error = %v", closeErr)
		}
	})

	writePollingFile(t, root, "zz_spice_gen.go", "package main\n")
	writePollingFile(t, root, "openapi.json", "{}")
	writePollingFile(t, root, ".spice/app.manifest.json", "{}")
	writePollingFile(t, root, ".main.go.swp", "temporary")
	writePollingFile(t, root, "vendor/example/module.go", "package example\n")
	clock.Advance(time.Second)
	assertNoWatchEvent(t, watcher.Events())
}

func TestPollingWatcherRejectsInvalidConfigurationAndBounds(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writePollingFile(t, root, "main.go", "package main\n")
	tests := []PollingConfig{
		{},
		{Root: filepath.Join(root, "missing")},
		{Root: filepath.Join(root, "main.go")},
		{Root: root, Interval: -time.Second},
		{Root: root, Maximum: -1},
	}
	for _, config := range tests {
		if _, err := NewPollingWatcher(
			context.Background(),
			config,
			nil,
		); err == nil {
			t.Fatalf("NewPollingWatcher(%+v) error = nil, want failure", config)
		}
	}
	if _, err := NewPollingWatcher(
		context.Background(),
		PollingConfig{Root: root, Maximum: 1},
		nil,
	); err != nil {
		t.Fatalf("NewPollingWatcher(maximum boundary) error = %v", err)
	}
	writePollingFile(t, root, "second.go", "package main\n")
	if _, err := NewPollingWatcher(
		context.Background(),
		PollingConfig{Root: root, Maximum: 1},
		nil,
	); err == nil {
		t.Fatal("NewPollingWatcher(over maximum) error = nil, want failure")
	}
}

func writePollingFile(
	t *testing.T,
	root string,
	relativePath string,
	content string,
) {
	t.Helper()
	filePath := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(filePath), 0o750); err != nil {
		t.Fatalf("MkdirAll(%s) error = %v", relativePath, err)
	}
	if err := os.WriteFile(filePath, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%s) error = %v", relativePath, err)
	}
}

func assertWatchEvent(
	t *testing.T,
	events <-chan FileEvent,
	want FileEvent,
) {
	t.Helper()
	select {
	case got := <-events:
		if got != want {
			t.Fatalf("watch event = %+v, want %+v", got, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for watch event %+v", want)
	}
}

func assertNoWatchEvent(t *testing.T, events <-chan FileEvent) {
	t.Helper()
	select {
	case event := <-events:
		t.Fatalf("unexpected watch event %+v", event)
	default:
	}
}
