package release

import (
	"slices"
	"testing"
)

func TestParseTargetsNormalizesAndRejectsInvalidInput(t *testing.T) {
	t.Parallel()
	targets, err := ParseTargets("windows/arm64, linux/amd64")
	if err != nil {
		t.Fatal(err)
	}
	want := []Target{
		{GOOS: "linux", GOARCH: "amd64"},
		{GOOS: "windows", GOARCH: "arm64"},
	}
	if !slices.Equal(targets, want) {
		t.Fatalf("ParseTargets() = %#v, want %#v", targets, want)
	}
	for _, value := range []string{
		"",
		"linux",
		"plan9/amd64",
		"linux/amd64,linux/amd64",
	} {
		if _, err := ParseTargets(value); err == nil {
			t.Errorf("ParseTargets(%q) error = nil", value)
		}
	}
}

func TestTargetPlatformConventions(t *testing.T) {
	t.Parallel()
	windows := Target{GOOS: "windows", GOARCH: "amd64"}
	if windows.ExecutableName() != "spice.exe" ||
		windows.ArchiveExtension() != ".zip" {
		t.Fatalf("Windows target = %#v", windows)
	}
	linux := Target{GOOS: "linux", GOARCH: "arm64"}
	if linux.ExecutableName() != "spice" ||
		linux.ArchiveExtension() != ".tar.gz" {
		t.Fatalf("Linux target = %#v", linux)
	}
}
