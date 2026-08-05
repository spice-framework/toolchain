// Package moduleenv derives fail-closed Go module execution settings.
package moduleenv

import (
	"os"
	"path/filepath"
	"strings"
)

// OfflineMode selects the vendor tree Go itself can use for root. A module's
// vendor directory is not valid while a parent go.work is active; workspace
// mode requires a vendor directory beside that go.work.
func OfflineMode(root string, environment []string) string {
	vendorRoot := root
	if workspace := activeWorkspace(root, environment); workspace != "" {
		vendorRoot = filepath.Dir(workspace)
	}
	if info, err := os.Stat(
		filepath.Join(vendorRoot, "vendor", "modules.txt"),
	); err == nil && !info.IsDir() {
		return "vendor"
	}
	return "readonly"
}

func activeWorkspace(root string, environment []string) string {
	for _, entry := range environment {
		name, value, found := strings.Cut(entry, "=")
		if !found || !strings.EqualFold(name, "GOWORK") {
			continue
		}
		if strings.EqualFold(value, "off") {
			return ""
		}
		if value != "" {
			return filepath.Clean(value)
		}
		break
	}
	current, err := filepath.Abs(root)
	if err != nil {
		return ""
	}
	for {
		workspace := filepath.Join(current, "go.work")
		if info, statErr := os.Stat(workspace); statErr == nil && !info.IsDir() {
			return workspace
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
}
