package resolve

import (
	"context"
	"testing"

	"github.com/spice-framework/toolchain/compiler/load"
)

func TestAnnotationsExcludeAuxiliaryPackageComments(t *testing.T) {
	root := writeModule(t, map[string]string{
		"go.mod": "module example.com/auxiliary\n\ngo 1.26.0\n",
		"extension/extension.go": `// Package extension is compiler support.
//
// @Module
package extension

type Client struct{}

// @Bean
func New() *Client {
	return &Client{}
}
`,
		"app/app.go": `package app

import "example.com/auxiliary/extension"

// @Application
func Application(*extension.Client) {}
`,
	})
	program, err := load.Load(
		context.Background(),
		load.Options{
			Dir:               root,
			AuxiliaryPackages: []string{"example.com/auxiliary/extension"},
		},
		"./app",
	)
	if err != nil {
		t.Fatalf("load.Load() error = %v", err)
	}

	result := Annotations(program)
	if len(result.Diagnostics) != 0 ||
		result.Files != 1 ||
		len(result.Occurrences) != 1 ||
		result.Occurrences[0].Annotation.Name != "Application" ||
		result.Occurrences[0].PackagePath != "example.com/auxiliary/app" {
		t.Fatalf("Annotations() = %#v", result)
	}
}
