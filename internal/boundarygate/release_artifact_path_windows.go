//go:build windows

package boundarygate

import (
	"errors"
	"path/filepath"
	"strings"
)

func validateReleaseExecutionBoundary(acknowledgement string) error {
	if acknowledgement != "1" {
		return errors.New("windows installed-byte verification requires the explicit ephemeral-runner acknowledgement")
	}
	return nil
}

func normalizeReleaseArtifactDirectory(directory string) (string, error) {
	// GitHub's Windows runner.temp value uses native separators, while a fixed
	// workflow suffix may arrive with forward slashes. Normalize only separator
	// direction here; filepath.Clean must not make any further change, so dot
	// segments and doubled separators remain rejected.
	directory = strings.ReplaceAll(directory, "/", `\`)
	if directory == "" || strings.HasPrefix(directory, `\\`) ||
		!filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		return "", errors.New("verified artifact directory must be canonical and absolute on a local volume")
	}
	volume := filepath.VolumeName(directory)
	if len(volume) != 2 || volume[1] != ':' {
		return "", errors.New("verified artifact directory must be canonical and absolute on a local volume")
	}
	return directory, nil
}
