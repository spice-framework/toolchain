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
				"go.mod": "module github.com/StevenBuglione/spice\n\ngo 1.23.0\n",
				"lifecycle/cleanup.go": `package lifecycle
import "context"
type Cleanup func(context.Context) error
`,
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

func TestRunVerifyRejectsWrongShapeCanonicalCleanupReplacement(t *testing.T) {
	t.Parallel()
	root := writeModule(t, map[string]string{
		"go.mod": `module example.com/application


go 1.23.0

require github.com/StevenBuglione/spice v0.0.0
replace github.com/StevenBuglione/spice => ./fake-spice
`,
		"fake-spice/go.mod": `module github.com/StevenBuglione/spice


go 1.23.0
`,
		"fake-spice/lifecycle/cleanup.go": `package lifecycle

// Cleanup deliberately has the canonical path and name but the wrong shape.
type Cleanup func() error
`,
		"app/providers.go": `package app

import life "github.com/StevenBuglione/spice/lifecycle"

type Value struct{}

// @Bean
func Provider() (Value, life.Cleanup) { panic("provider and cleanup bodies must not execute") }
`,
	})

	code, stdout, stderr := runModule(root, "verify", "./app")
	if code != 1 || stdout != "" {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	for _, expected := range []string{"Provider", "second result must be lifecycle.Cleanup or error", "accepted forms are"} {
		if !strings.Contains(stderr, expected) {
			t.Fatalf("stderr=%q missing %q", stderr, expected)
		}
	}
}
