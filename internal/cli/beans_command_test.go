package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImportedAutoConfigurationGeneratesAndExplainsDefault(t *testing.T) {
	root := autoConfigurationCLIModule(t, false, false)
	code, stdout, stderr := runModule(root, "verify", "./app")
	if code != 0 || !strings.Contains(stdout, "verification passed") || stderr != "" {
		t.Fatalf("verify: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runModule(
		root,
		"beans",
		"--explain",
		"./app",
	)
	if code != 0 ||
		!strings.Contains(stdout, "selected") ||
		!strings.Contains(stdout, "example.com/cli-generation/client/autoconfigure.DefaultClient") ||
		!strings.Contains(stdout, "docs/client-review.md") ||
		stderr != "" {
		t.Fatalf("beans: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = runModule(root, "generate", "./app")
	if code != 0 || stderr != "" {
		t.Fatalf("generate: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	content, err := os.ReadFile(filepath.Join(
		root,
		"internal",
		"spicegen",
		"application",
		"sources",
		"client",
		"autoconfigure",
		"config_spice_gen.go",
	))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "autoconfigure.DefaultClient()") {
		t.Fatalf("generated auto-configuration adapter:\n%s", content)
	}
	code, stdout, stderr = runModule(
		root,
		"beans",
		"--explain",
		"--format=json",
		"./app",
	)
	if code != 0 ||
		!strings.Contains(stdout, `"schema": "spice.beans/v1"`) ||
		!strings.Contains(stdout, `"status": "selected"`) ||
		stderr != "" {
		t.Fatalf("beans JSON: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestApplicationBeanReplacesImportedDefaultBeforeConstruction(t *testing.T) {
	root := autoConfigurationCLIModule(t, true, false)
	code, stdout, stderr := runModule(root, "beans", "--explain", "./app")
	if code != 0 ||
		!strings.Contains(stdout, "replaced") ||
		!strings.Contains(stdout, "application bean applicationClient") ||
		stderr != "" {
		t.Fatalf("beans: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, _, stderr = runModule(root, "generate", "./app")
	if code != 0 || stderr != "" {
		t.Fatalf("generate: code=%d stderr=%q", code, stderr)
	}
	autoSource := filepath.Join(
		root,
		"internal",
		"spicegen",
		"application",
		"sources",
		"client",
		"autoconfigure",
		"config_spice_gen.go",
	)
	if _, err := os.Stat(autoSource); !os.IsNotExist(err) {
		t.Fatalf("replaced default retained generated source %s: %v", autoSource, err)
	}
	applicationSource, err := os.ReadFile(filepath.Join(
		root,
		"internal",
		"spicegen",
		"application",
		"sources",
		"app",
		"application_spice_gen.go",
	))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(applicationSource), "ApplicationClient") {
		t.Fatalf("generated application provider source:\n%s", applicationSource)
	}
}

func TestImportedDefaultBacksOffWhenRequiredInputIsUnavailable(t *testing.T) {
	root := autoConfigurationCLIModule(t, false, true)
	code, stdout, stderr := runModule(root, "beans", "--explain", "./app")
	if code != 0 ||
		!strings.Contains(stdout, "inactive") ||
		!strings.Contains(stdout, "client.Options") ||
		stderr != "" {
		t.Fatalf("beans: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestRetiredStarterSelectionFailsWithImportMigration(t *testing.T) {
	root := generationCLIModule(t, generationApplicationSource)
	path := filepath.Join(root, ".spice", "starters.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runModule(root, "verify", "./app")
	if code != 1 ||
		stdout != "" ||
		!strings.Contains(stderr, "retired starter selection") ||
		!strings.Contains(stderr, "blank-import") {
		t.Fatalf("verify: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestBeansCommandValidatesArgumentsAndHandlesNoImports(t *testing.T) {
	root := generationCLIModule(t, generationApplicationSource)
	tests := []struct {
		arguments []string
		want      string
	}{
		{arguments: []string{"beans"}, want: "requires --explain"},
		{
			arguments: []string{"beans", "--explain", "--format"},
			want:      "--format requires",
		},
		{
			arguments: []string{"beans", "--explain", "--format=yaml"},
			want:      "unsupported beans format",
		},
		{
			arguments: []string{"beans", "--explain", "--unknown"},
			want:      "unknown beans option",
		},
	}
	for _, test := range tests {
		code, stdout, stderr := runModule(root, test.arguments...)
		if code != 2 || stdout != "" || !strings.Contains(stderr, test.want) {
			t.Fatalf(
				"%v: code=%d stdout=%q stderr=%q",
				test.arguments,
				code,
				stdout,
				stderr,
			)
		}
	}
	code, stdout, stderr := runModule(root, "beans", "--explain", "./app")
	if code != 0 ||
		!strings.Contains(stdout, "none explicitly imported") ||
		stderr != "" {
		t.Fatalf("beans without imports: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func autoConfigurationCLIModule(
	t *testing.T,
	override bool,
	requiresOptions bool,
) string {
	t.Helper()
	applicationProvider := ""
	applicationParameter := "*client.Client"
	applicationImports := `"example.com/cli-generation/client"
	_ "example.com/cli-generation/client/autoconfigure"`
	if override {
		applicationProvider = `
// @Bean
func ApplicationClient() *client.Client { return &client.Client{} }
`
	}
	if requiresOptions {
		applicationParameter = ""
		applicationImports = `_ "example.com/cli-generation/client/autoconfigure"`
	}
	source := `package app

import (
	` + applicationImports + `
)
` + applicationProvider + `
// @Application
func Application(` + applicationParameter + `) {}
`
	root := generationCLIModule(t, source)
	if err := os.MkdirAll(filepath.Join(root, "client", "autoconfigure"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(root, "client", "client.go"),
		[]byte("package client\n\ntype Client struct{}\ntype Options struct{}\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	factoryParameter := ""
	if requiresOptions {
		factoryParameter = "client.Options"
	}
	configuration := `package autoconfigure

import (
	"example.com/cli-generation/client"
	"github.com/spice-framework/spice/starter"
)

func DefaultClient(` + factoryParameter + `) *client.Client { return &client.Client{} }

func SpiceAutoConfiguration() starter.AutoConfiguration {
	return starter.AutoConfiguration{
		Review: "docs/client-review.md",
		Beans: []starter.AutoBean{{
			Factory: DefaultClient,
			Fallback: true,
		}},
	}
}
`
	if err := os.WriteFile(
		filepath.Join(root, "client", "autoconfigure", "config.go"),
		[]byte(configuration),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	return root
}
