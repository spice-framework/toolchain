//go:build windows

package releaseinstallation

import (
	"path/filepath"
	"strings"
)

func isCanonicalLocalPath(value string) bool {
	if value == "" || strings.HasPrefix(value, `\\`) || strings.HasPrefix(value, "//") ||
		!filepath.IsAbs(value) || filepath.Clean(value) != value {
		return false
	}
	volume := filepath.VolumeName(value)
	return len(volume) == 2 && volume[1] == ':'
}
