//go:build !windows

package boundarygate

import (
	"errors"
	"path/filepath"
)

func validateReleaseExecutionBoundary(acknowledgement string) error {
	if acknowledgement != "" {
		return errors.New("ephemeral-runner acknowledgement is valid only on Windows")
	}
	return nil
}

func normalizeReleaseArtifactDirectory(directory string) (string, error) {
	if directory == "" || !filepath.IsAbs(directory) || filepath.Clean(directory) != directory {
		return "", errors.New("verified artifact directory must be canonical and absolute")
	}
	return directory, nil
}
