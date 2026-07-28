package cli

import (
	"strings"
	"testing"
)

func TestRunVerifyTreatsFirstCleanupResultAsProvidedOutput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		declarations string
		signature    string
	}{
		{
			name:      "canonical",
			signature: "(life.Cleanup, life.Cleanup, error)",
		},
		{
			name: "aliases",
			declarations: `type OutputAlias = life.Cleanup
	type MetadataAlias = life.Cleanup
	type ErrorAlias = error`,
			signature: "(OutputAlias, MetadataAlias, ErrorAlias)",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := writeModule(t, map[string]string{
				"go.mod": "module example.com/cleanup\n\ngo 1.26.0\n",
				"app/providers.go": `package app

import life "github.com/StevenBuglione/spice/lifecycle"

` + test.declarations + `

// @Bean
func Provider() ` + test.signature + ` {
	panic("provider and cleanup bodies must not execute")
}
`,
			})
			code, stdout, stderr := runModule(root, "verify", "./app")
			if code != 0 || !strings.Contains(stdout, "1 annotations") || stderr != "" {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
			}
		})
	}
}
