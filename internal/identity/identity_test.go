package identity

import (
	"strings"
	"testing"
)

func TestOfficialToolIdentitiesAreDistinctToolchainPackages(t *testing.T) {
	t.Parallel()
	if CLITool == AnnotationTool {
		t.Fatal("CLI and annotation tools must have distinct package identities")
	}
	for name, path := range map[string]string{
		"CLI":        CLITool,
		"annotation": AnnotationTool,
	} {
		if !strings.HasPrefix(path, ToolchainModule+"/cmd/") {
			t.Fatalf("%s tool %q is outside %s", name, path, ToolchainModule)
		}
	}
}

func TestNormalizeDescriptorToolIsLimitedToOfficialCoreDescriptors(t *testing.T) {
	t.Parallel()
	for name, test := range map[string]struct {
		descriptor string
		tool       string
		want       string
	}{
		"legacy official": {
			descriptor: CoreModule + "/annotation/core",
			tool:       LegacyAnnotationTool,
			want:       AnnotationTool,
		},
		"current official": {
			descriptor: CoreModule + "/annotation/web",
			tool:       AnnotationTool,
			want:       AnnotationTool,
		},
		"third party": {
			descriptor: "example.com/mail/annotation",
			tool:       LegacyAnnotationTool,
			want:       LegacyAnnotationTool,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := NormalizeDescriptorTool(test.descriptor, test.tool); got != test.want {
				t.Fatalf("NormalizeDescriptorTool() = %q, want %q", got, test.want)
			}
		})
	}
}
