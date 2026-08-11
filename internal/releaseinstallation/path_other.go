//go:build !windows

package releaseinstallation

import "path/filepath"

func isCanonicalLocalPath(value string) bool {
	return value != "" && filepath.IsAbs(value) && filepath.Clean(value) == value
}
