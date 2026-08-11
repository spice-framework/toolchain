//go:build windows

package releaseinstallation

import "testing"

func TestCanonicalLocalPathRejectsWindowsNetworkAndDevicePaths(t *testing.T) {
	t.Parallel()
	if !isCanonicalLocalPath(`C:\verified\subjects`) {
		t.Fatal("canonical drive path was rejected")
	}
	for _, value := range []string{
		`\\server\share\subjects`,
		`\\?\C:\verified\subjects`,
		`\\.\C:\verified\subjects`,
		`\subjects`,
	} {
		if isCanonicalLocalPath(value) {
			t.Fatalf("Windows non-local path %q was accepted", value)
		}
	}
}
