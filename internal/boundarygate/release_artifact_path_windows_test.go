//go:build windows

package boundarygate

import "testing"

func TestReleaseArtifactDirectoryRejectsWindowsNetworkAndDevicePaths(t *testing.T) {
	t.Parallel()
	if _, err := normalizeReleaseArtifactDirectory(`C:\verified\subjects`); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{
		`\\server\share\subjects`,
		`\\?\C:\verified\subjects`,
		`\\.\C:\verified\subjects`,
		`\subjects`,
	} {
		if _, err := normalizeReleaseArtifactDirectory(value); err == nil {
			t.Fatalf("Windows non-local path %q was accepted", value)
		}
	}
}
