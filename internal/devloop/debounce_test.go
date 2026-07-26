package devloop

import (
	"testing"
	"time"
)

func TestDebouncerUsesQuietPeriodAndMaximumDelay(t *testing.T) {
	t.Parallel()
	start := time.Unix(1_000, 0)
	debouncer, err := NewDebouncer(100*time.Millisecond, 250*time.Millisecond)
	if err != nil {
		t.Fatalf("NewDebouncer() error = %v", err)
	}
	if err := debouncer.Add(start, FileEvent{Path: "b.go", Kind: ChangeWrite}); err != nil {
		t.Fatalf("Add(first) error = %v", err)
	}
	if err := debouncer.Add(
		start.Add(90*time.Millisecond),
		FileEvent{Path: "a.go", Kind: ChangeCreate},
	); err != nil {
		t.Fatalf("Add(second) error = %v", err)
	}
	if err := debouncer.Add(
		start.Add(180*time.Millisecond),
		FileEvent{Path: "a.go", Kind: ChangeWrite},
	); err != nil {
		t.Fatalf("Add(third) error = %v", err)
	}

	deadline, found := debouncer.Deadline()
	if !found {
		t.Fatal("Deadline() found = false, want true")
	}
	wantDeadline := start.Add(250 * time.Millisecond)
	if !deadline.Equal(wantDeadline) {
		t.Fatalf("Deadline() = %v, want %v", deadline, wantDeadline)
	}
	if _, ready := debouncer.Take(
		wantDeadline.Add(-time.Nanosecond),
		7,
	); ready {
		t.Fatal("Take(before deadline) ready = true, want false")
	}
	batch, ready := debouncer.Take(wantDeadline, 7)
	if !ready {
		t.Fatal("Take(deadline) ready = false, want true")
	}
	if batch.Revision() != 7 {
		t.Fatalf("Revision() = %d, want 7", batch.Revision())
	}
	want := []FileEvent{
		{Path: "a.go", Kind: ChangeCreate},
		{Path: "b.go", Kind: ChangeWrite},
	}
	assertFileEvents(t, batch.Changes(), want)
	if debouncer.Pending() {
		t.Fatal("Pending() = true after Take, want false")
	}
}

func TestDebouncerCoalescesAtomicSaveAndRemoval(t *testing.T) {
	t.Parallel()
	now := time.Unix(2_000, 0)
	debouncer, err := NewDebouncer(time.Second, 2*time.Second)
	if err != nil {
		t.Fatalf("NewDebouncer() error = %v", err)
	}
	events := []FileEvent{
		{Path: "main.go", Kind: ChangeRemove},
		{Path: "main.go", Kind: ChangeCreate},
		{Path: "obsolete.go", Kind: ChangeWrite},
		{Path: "obsolete.go", Kind: ChangeRemove},
	}
	for _, event := range events {
		if err := debouncer.Add(now, event); err != nil {
			t.Fatalf("Add(%+v) error = %v", event, err)
		}
	}
	batch, ready := debouncer.Take(now.Add(time.Second), 1)
	if !ready {
		t.Fatal("Take() ready = false, want true")
	}
	assertFileEvents(t, batch.Changes(), []FileEvent{
		{Path: "main.go", Kind: ChangeWrite},
		{Path: "obsolete.go", Kind: ChangeRemove},
	})
}

func TestDebouncerRejectsInvalidConfigurationAndEvents(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		quiet time.Duration
		max   time.Duration
	}{
		{name: "zero quiet", max: time.Second},
		{name: "negative quiet", quiet: -time.Second, max: time.Second},
		{name: "short maximum", quiet: time.Second, max: time.Millisecond},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := NewDebouncer(test.quiet, test.max); err == nil {
				t.Fatal("NewDebouncer() error = nil, want failure")
			}
		})
	}

	debouncer, err := NewDebouncer(time.Second, time.Second)
	if err != nil {
		t.Fatalf("NewDebouncer() error = %v", err)
	}
	if err := debouncer.Add(time.Time{}, FileEvent{Kind: ChangeWrite}); err == nil {
		t.Fatal("Add(empty path) error = nil, want failure")
	}
	if err := debouncer.Add(
		time.Time{},
		FileEvent{Path: "main.go", Kind: "invalid"},
	); err == nil {
		t.Fatal("Add(invalid kind) error = nil, want failure")
	}
}

func TestBatchReturnsDefensiveChanges(t *testing.T) {
	t.Parallel()
	debouncer, err := NewDebouncer(time.Second, time.Second)
	if err != nil {
		t.Fatalf("NewDebouncer() error = %v", err)
	}
	if err := debouncer.Add(
		time.Time{},
		FileEvent{Path: "main.go", Kind: ChangeWrite},
	); err != nil {
		t.Fatalf("Add() error = %v", err)
	}
	batch, ready := debouncer.Take(time.Time{}.Add(time.Second), 1)
	if !ready {
		t.Fatal("Take() ready = false, want true")
	}
	first := batch.Changes()
	first[0].Path = "changed.go"
	second := batch.Changes()
	if second[0].Path != "main.go" {
		t.Fatalf("Changes() path = %q, want main.go", second[0].Path)
	}
}

func BenchmarkDebouncerBurst(b *testing.B) {
	start := time.Unix(1_000, 0)
	for range b.N {
		debouncer, err := NewDebouncer(
			100*time.Millisecond,
			2*time.Second,
		)
		if err != nil {
			b.Fatalf("NewDebouncer() error = %v", err)
		}
		for index := range 100 {
			event := FileEvent{
				Path: "module/file.go",
				Kind: ChangeWrite,
			}
			if index%2 == 0 {
				event.Path = "module/config.yaml"
			}
			if err := debouncer.Add(
				start.Add(time.Duration(index)*time.Millisecond),
				event,
			); err != nil {
				b.Fatalf("Add() error = %v", err)
			}
		}
		if _, ready := debouncer.Take(start.Add(2*time.Second), 1); !ready {
			b.Fatal("Take() ready = false, want true")
		}
	}
}

func assertFileEvents(t *testing.T, got, want []FileEvent) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("events length = %d, want %d: %+v", len(got), len(want), got)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("event[%d] = %+v, want %+v", index, got[index], want[index])
		}
	}
}
