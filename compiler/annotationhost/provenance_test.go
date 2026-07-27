package annotationhost

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/StevenBuglione/spice/annotation"
)

func TestValidateDescriptorToolModule(t *testing.T) {
	replacement := filepath.Join(t.TempDir(), "plugin")
	descriptor := annotation.ModuleProvenance{
		Path:             "example.com/plugin",
		Version:          "v1.4.0",
		ReplacementPath:  replacement,
		ReplacementDir:   replacement,
		LocalReplacement: true,
	}
	tool := PackageIdentity{
		Path: "example.com/plugin/cmd/annotations",
		Module: ModuleIdentity{
			Path:    "example.com/plugin",
			Version: "v1.4.0",
			Replacement: &ModuleIdentity{
				Path:      replacement,
				Directory: replacement,
			},
		},
	}
	if err := ValidateDescriptorToolModule(descriptor, tool); err != nil {
		t.Fatalf("ValidateDescriptorToolModule() error = %v", err)
	}
	for _, change := range []func(*PackageIdentity){
		func(value *PackageIdentity) { value.Module.Version = "v1.5.0" },
		func(value *PackageIdentity) {
			value.Module.Replacement.Directory = filepath.Join(
				replacement,
				"other",
			)
		},
	} {
		changed := clonePackageIdentity(tool)
		change(&changed)
		err := ValidateDescriptorToolModule(descriptor, changed)
		if err == nil || !strings.Contains(err.Error(), "does not match") &&
			!strings.Contains(err.Error(), "different replacements") {
			t.Fatalf("ValidateDescriptorToolModule() error = %v", err)
		}
	}
}
