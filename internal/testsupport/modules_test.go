package testsupport

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestCoreDirectoryResolvesAndCachesPinnedModule(t *testing.T) {
	const callers = 8
	results := make(chan string, callers)
	var group sync.WaitGroup
	for range callers {
		group.Add(1)
		go func() {
			defer group.Done()
			results <- CoreDirectory(t)
		}()
	}
	group.Wait()
	close(results)

	var selected string
	for result := range results {
		if selected == "" {
			selected = result
		}
		if result != selected {
			t.Fatalf("CoreDirectory() returned inconsistent paths %q and %q", selected, result)
		}
	}
	if !filepath.IsAbs(selected) {
		t.Fatalf("CoreDirectory() = %q, want absolute path", selected)
	}
	content, err := os.ReadFile(filepath.Join(selected, "go.mod"))
	if err != nil {
		t.Fatalf("read selected go.mod: %v", err)
	}
	if !strings.Contains(string(content), "module github.com/spice-framework/spice\n") {
		t.Fatalf("selected go.mod does not identify core: %s", content)
	}
}
