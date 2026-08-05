package annotationhost

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestModuleIdentityPreservesReplacementProvenance(t *testing.T) {
	t.Parallel()

	if got := moduleIdentity(nil); got != (ModuleIdentity{}) {
		t.Fatalf("moduleIdentity(nil) = %+v", got)
	}
	got := moduleIdentity(&goListModule{
		Path:    "example.com/descriptor",
		Version: "v1.2.3",
		Dir:     filepath.Join("cache", "descriptor"),
		Replace: &goListModule{
			Path:    "example.com/descriptor-fork",
			Version: "v1.2.4",
			Dir:     filepath.Join("workspace", "descriptor"),
		},
	})
	if got.Path != "example.com/descriptor" || got.Version != "v1.2.3" ||
		got.Replacement == nil ||
		got.Replacement.Path != "example.com/descriptor-fork" ||
		got.Replacement.Version != "v1.2.4" {
		t.Fatalf("moduleIdentity() = %+v", got)
	}
}

func TestOfflineEnvironmentReplacesEveryModuleModeSpelling(t *testing.T) {
	t.Parallel()

	original := []string{
		"PATH=fixture",
		"GOPROXY=https://proxy.invalid",
		"GOFLAGS=-race -mod vendor -tags=integration -mod=readonly",
	}
	got := offlineEnvironment(original, "vendor")
	if !slices.Contains(got, "GOPROXY=off") ||
		!slices.Contains(got, "GOFLAGS=-race -tags=integration -mod=vendor") {
		t.Fatalf("offlineEnvironment() = %v", got)
	}
	for _, value := range got {
		if strings.Contains(value, "proxy.invalid") || strings.Contains(value, "-mod=readonly") {
			t.Fatalf("stale online/module mode retained: %q", value)
		}
	}
	if original[1] != "GOPROXY=https://proxy.invalid" {
		t.Fatalf("offlineEnvironment() mutated input: %v", original)
	}

	withoutFlags := offlineEnvironment([]string{"PATH=fixture"}, "readonly")
	if !slices.Contains(withoutFlags, "GOFLAGS=-mod=readonly") {
		t.Fatalf("offlineEnvironment(no flags) = %v", withoutFlags)
	}
}
