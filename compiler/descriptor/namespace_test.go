package descriptor

import (
	"reflect"
	"strings"
	"testing"

	"github.com/StevenBuglione/spice/annotation"
)

func TestNamespaceReferencesDiscoversOnlyStaticDescriptorSignatures(
	t *testing.T,
) {
	t.Parallel()
	program := loadDescriptorProgram(t, map[string]string{
		"defs/controller.go": validDescriptorSource(
			"Controller",
			"web.Controller",
		),
		"defs/other.go": `package defs

import "github.com/StevenBuglione/spice/annotation/sdk"

func Helper(string) sdk.Definition {
	return sdk.Definition{}
}

func hidden() sdk.Definition {
	return sdk.Definition{}
}
`,
	})
	got, err := NamespaceReferences(
		program,
		[]string{"example.com/plugin/defs"},
	)
	if err != nil {
		t.Fatalf("NamespaceReferences() error = %v", err)
	}
	want := []annotation.DefinitionReference{{
		Package: "example.com/plugin/defs",
		Symbol:  "Controller",
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("NamespaceReferences() = %#v, want %#v", got, want)
	}
}

func TestNamespaceReferencesFailsClosed(t *testing.T) {
	t.Parallel()
	if _, err := NamespaceReferences(
		nil,
		[]string{"example.com/plugin/defs"},
	); err == nil || !strings.Contains(err.Error(), "program is nil") {
		t.Fatalf("NamespaceReferences(nil) error = %v", err)
	}
	program := loadDescriptorProgram(t, map[string]string{
		"defs/helper.go": `package defs

func Helper() {}
`,
	})
	if _, err := NamespaceReferences(
		program,
		[]string{"example.com/plugin/defs"},
	); err == nil || !strings.Contains(err.Error(), "exports no") {
		t.Fatalf("NamespaceReferences(no descriptors) error = %v", err)
	}
}
