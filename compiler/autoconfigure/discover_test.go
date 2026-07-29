package autoconfigure

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/StevenBuglione/spice/compiler/load"
)

func TestDiscoverAndDecodeSelectedTypedConfiguration(t *testing.T) {
	root := autoConfigurationModule(t, map[string]string{
		"app/app.go": `package app

import _ "example.com/auto/client/autoconfigure"
`,
		"client/client.go": `package client

type Client struct{}
`,
		"client/autoconfigure/config.go": `package autoconfigure

import (
	"example.com/auto/client"
	"github.com/StevenBuglione/spice/starter"
)

func DefaultClient() *client.Client { return &client.Client{} }

func SpiceAutoConfiguration() starter.AutoConfiguration {
	return starter.AutoConfiguration{
		Review: "docs/client-review.md",
		Beans: []starter.AutoBean{{
			Factory: DefaultClient,
			Name: "client",
			Aliases: []string{"defaultClient"},
			Qualifiers: []string{"local"},
			Fallback: true,
			Order: 7,
		}},
	}
}
`,
	})
	discovery, err := Discover(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantPackages := []string{"example.com/auto/client/autoconfigure"}
	if !slices.Equal(discovery.Packages, wantPackages) {
		t.Fatalf("Discover().Packages = %v, want %v", discovery.Packages, wantPackages)
	}
	program, err := load.Load(
		t.Context(),
		load.Options{
			Dir:               root,
			AuxiliaryPackages: discovery.Packages,
		},
		"./app",
	)
	if err != nil {
		t.Fatal(err)
	}
	selected := discovery.Selected(program)
	if !slices.Equal(selected, wantPackages) {
		t.Fatalf("Selected() = %v, want %v", selected, wantPackages)
	}
	configurations, err := Decode(program, selected)
	if err != nil {
		t.Fatal(err)
	}
	if len(configurations) != 1 || len(configurations[0].Beans) != 1 {
		t.Fatalf("Decode() = %+v", configurations)
	}
	bean := configurations[0].Beans[0]
	if bean.Factory != "DefaultClient" ||
		bean.Name != "client" ||
		!bean.Fallback ||
		bean.Order != 7 ||
		!slices.Equal(bean.Aliases, []string{"defaultClient"}) ||
		!slices.Equal(bean.Qualifiers, []string{"local"}) {
		t.Fatalf("decoded bean = %+v", bean)
	}
}

func TestDecodeRejectsExecutableAndForeignDescriptors(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "control flow",
			body: `if true {
	return starter.AutoConfiguration{}
}
return starter.AutoConfiguration{}`,
			want: "exactly one returned composite literal",
		},
		{
			name: "foreign factory",
			body: `return starter.AutoConfiguration{
	Review: "review.md",
	Beans: []starter.AutoBean{{Factory: client.New}},
}`,
			want: "factory must reference a function in the autoconfigure package",
		},
		{
			name: "computed descriptor",
			body: "return helper()",
			want: "must be a starter.AutoConfiguration composite literal",
		},
		{
			name: "missing review",
			body: `return starter.AutoConfiguration{
	Beans: []starter.AutoBean{{Factory: Local}},
}`,
			want: "Review must be a string constant",
		},
		{
			name: "empty review",
			body: `return starter.AutoConfiguration{
	Review: "",
	Beans: []starter.AutoBean{{Factory: Local}},
}`,
			want: "Review must be a non-empty trimmed string constant",
		},
		{
			name: "missing beans",
			body: `return starter.AutoConfiguration{Review: "review.md"}`,
			want: "requires a non-empty Beans field",
		},
		{
			name: "empty beans",
			body: `return starter.AutoConfiguration{
	Review: "review.md",
	Beans: []starter.AutoBean{},
}`,
			want: "Beans must be a non-empty composite literal",
		},
		{
			name: "dynamic bean",
			body: `return starter.AutoConfiguration{
	Review: "review.md",
	Beans: []starter.AutoBean{configuredBean},
}`,
			want: "must be a starter.AutoBean composite literal",
		},
		{
			name: "missing factory",
			body: `return starter.AutoConfiguration{
	Review: "review.md",
	Beans: []starter.AutoBean{{Fallback: true}},
}`,
			want: "factory is required",
		},
		{
			name: "unexported factory",
			body: `return starter.AutoConfiguration{
	Review: "review.md",
	Beans: []starter.AutoBean{{Factory: local}},
}`,
			want: "factory must reference an exported function",
		},
		{
			name: "primary fallback",
			body: `return starter.AutoConfiguration{
	Review: "review.md",
	Beans: []starter.AutoBean{{
		Factory: Local,
		Primary: true,
		Fallback: true,
	}},
}`,
			want: "primary and fallback cannot both be true",
		},
		{
			name: "dynamic name",
			body: `return starter.AutoConfiguration{
	Review: "review.md",
	Beans: []starter.AutoBean{{
		Factory: Local,
		Name: configuredName,
	}},
}`,
			want: "Name must be a string constant",
		},
		{
			name: "dynamic aliases",
			body: `return starter.AutoConfiguration{
	Review: "review.md",
	Beans: []starter.AutoBean{{
		Factory: Local,
		Aliases: configuredAliases,
	}},
}`,
			want: "Aliases must be a string slice composite literal",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := autoConfigurationModule(t, map[string]string{
				"app/app.go": `package app
import _ "example.com/auto/client/autoconfigure"
`,
				"client/client.go": `package client
type Client struct{}
func New() *Client { return &Client{} }
`,
				"client/autoconfigure/config.go": `package autoconfigure
import (
	"example.com/auto/client"
	"github.com/StevenBuglione/spice/starter"
)
func Local() *client.Client { return client.New() }
func local() *client.Client { return client.New() }
var configuredBean = starter.AutoBean{Factory: Local}
var configuredName = "client"
var configuredAliases = []string{"client"}
func helper() starter.AutoConfiguration { return starter.AutoConfiguration{} }
func SpiceAutoConfiguration() starter.AutoConfiguration {
` + test.body + `
}
`,
			})
			discovery, err := Discover(root, nil)
			if err != nil {
				t.Fatal(err)
			}
			program, err := load.Load(
				t.Context(),
				load.Options{Dir: root, AuxiliaryPackages: discovery.Packages},
				"./app",
			)
			if err != nil {
				t.Fatal(err)
			}
			_, err = Decode(program, discovery.Selected(program))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Decode() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestDiscoverUsesOverlaysAndSelectionExcludesUnrequestedImports(t *testing.T) {
	root := autoConfigurationModule(t, map[string]string{
		"app/app.go": "package app\n",
		"other/other.go": `package other
import _ "example.com/auto/other/autoconfigure"
`,
		"client/autoconfigure/config.go": minimalAutoConfigurationSource("client"),
		"other/autoconfigure/config.go":  minimalAutoConfigurationSource("other"),
		".hidden/ignored/ignored.go": `package ignored
import _ "example.com/auto/ignored/autoconfigure"
`,
		"nested/go.mod": "module example.com/nested\n\ngo 1.26.0\n",
		"nested/ignored.go": `package nested
import _ "example.com/auto/nested/autoconfigure"
`,
	})
	appFile := filepath.Join(root, "app", "app.go")
	overlay := map[string][]byte{
		appFile: []byte(`package app
import _ "example.com/auto/client/autoconfigure"
`),
	}
	discovery, err := Discover(root, overlay)
	if err != nil {
		t.Fatal(err)
	}
	wantCandidates := []string{
		"example.com/auto/client/autoconfigure",
		"example.com/auto/other/autoconfigure",
	}
	if !slices.Equal(discovery.Packages, wantCandidates) {
		t.Fatalf("Discover().Packages = %v, want %v", discovery.Packages, wantCandidates)
	}
	program, err := load.Load(
		t.Context(),
		load.Options{
			Dir:               root,
			Overlay:           overlay,
			AuxiliaryPackages: discovery.Packages,
		},
		"./app",
	)
	if err != nil {
		t.Fatal(err)
	}
	wantSelected := []string{"example.com/auto/client/autoconfigure"}
	if selected := discovery.Selected(program); !slices.Equal(selected, wantSelected) {
		t.Fatalf("Selected() = %v, want %v", selected, wantSelected)
	}
}

func TestDecodeRequiresCanonicalPackageAndDescriptor(t *testing.T) {
	tests := []struct {
		name        string
		packageName string
		descriptor  string
		want        string
	}{
		{
			name:        "package",
			packageName: "configuration",
			descriptor: `func SpiceAutoConfiguration() starter.AutoConfiguration {
	return starter.AutoConfiguration{}
}`,
			want: `package name must be "autoconfigure"`,
		},
		{
			name:        "missing",
			packageName: "autoconfigure",
			want:        "must declare exactly one func SpiceAutoConfiguration",
		},
		{
			name:        "signature",
			packageName: "autoconfigure",
			descriptor: `func SpiceAutoConfiguration(value int) starter.AutoConfiguration {
	return starter.AutoConfiguration{}
}`,
			want: "must have exact signature",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := autoConfigurationModule(t, map[string]string{
				"app/app.go": `package app
import _ "example.com/auto/client/autoconfigure"
`,
				"client/autoconfigure/config.go": "package " + test.packageName + `
import "github.com/StevenBuglione/spice/starter"
var _ starter.AutoConfiguration
` + test.descriptor,
			})
			program, err := load.Load(
				t.Context(),
				load.Options{
					Dir:               root,
					AuxiliaryPackages: []string{"example.com/auto/client/autoconfigure"},
				},
				"./app",
			)
			if err != nil {
				t.Fatal(err)
			}
			_, err = Decode(program, []string{"example.com/auto/client/autoconfigure"})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Decode() error = %v, want %q", err, test.want)
			}
		})
	}
}

func minimalAutoConfigurationSource(name string) string {
	return `package autoconfigure
import "github.com/StevenBuglione/spice/starter"
type ` + name + ` struct{}
func Default() *` + name + ` { return &` + name + `{} }
func SpiceAutoConfiguration() starter.AutoConfiguration {
	return starter.AutoConfiguration{
		Review: "review.md",
		Beans: []starter.AutoBean{{Factory: Default}},
	}
}
`
}

func autoConfigurationModule(
	t *testing.T,
	files map[string]string,
) string {
	t.Helper()
	root := t.TempDir()
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	files["go.mod"] = "module example.com/auto\n\ngo 1.26.0\n\n" +
		"require github.com/StevenBuglione/spice v0.0.0\n\n" +
		"replace github.com/StevenBuglione/spice => " +
		filepath.ToSlash(repository) + "\n"
	for name, content := range files {
		filename := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(filename), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
