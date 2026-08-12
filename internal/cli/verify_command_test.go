package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spice-framework/toolchain/compiler/diagnostic"
	"github.com/spice-framework/toolchain/compiler/load"
)

func TestVerifyJSONSuccessAndValidationFailure(t *testing.T) {
	root := writeGoSource(t, `package sample

// @Service
type Service struct{}
`)
	code, stdout, stderr := runModule(root, "verify", "--format=json", ".")
	report := decodeDiagnosticReport(t, stdout)
	if code != 0 ||
		stderr != "" ||
		!report.Success ||
		len(report.Diagnostics) != 0 ||
		!strings.Contains(report.Summary, "1 annotations") {
		t.Fatalf(
			"success: code=%d report=%#v stderr=%q",
			code,
			report,
			stderr,
		)
	}

	invalidSource, _ := withTestAnnotationImports(
		`package sample

// @Controller(prefx="/users")
type Controller struct{}
`,
		false,
	)
	if err := os.WriteFile(
		filepath.Join(root, "sample.go"),
		[]byte(invalidSource),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = runModule(
		root,
		"verify",
		"--format",
		"json",
		".",
	)
	report = decodeDiagnosticReport(t, stdout)
	if code != 1 ||
		stderr != "" ||
		report.Success ||
		len(report.Diagnostics) != 1 ||
		report.Diagnostics[0].Code !=
			"spice.validation.unknown-argument" ||
		report.Diagnostics[0].Location.URI == "" ||
		report.Diagnostics[0].Location.Range.Start.Line != 3 {
		t.Fatalf(
			"failure: code=%d report=%#v stderr=%q",
			code,
			report,
			stderr,
		)
	}
}

func TestVerifyJSONConvertsLoadAndApplicationDiagnostics(t *testing.T) {
	tests := []struct {
		name   string
		source string
		code   string
	}{
		{
			name: "load",
			source: `package sample

var value Missing
`,
			code: "spice.load.type",
		},
		{
			name: "application",
			source: `package sample

type Missing struct{}

// @Application
func Application(Missing) {}
`,
			code: "spice.application.missing-root-provider",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := writeGoSource(t, test.source)
			code, stdout, stderr := runModule(
				root,
				"verify",
				"--format=json",
				".",
			)
			report := decodeDiagnosticReport(t, stdout)
			found := false
			for _, item := range report.Diagnostics {
				if item.Code == test.code {
					found = true
					break
				}
			}
			if code != 1 ||
				stderr != "" ||
				report.Success ||
				len(report.Diagnostics) == 0 ||
				!found {
				t.Fatalf(
					"code=%d report=%#v stderr=%q",
					code,
					report,
					stderr,
				)
			}
		})
	}
}

func TestVerifyTextUsesSharedDiagnosticCode(t *testing.T) {
	root := writeGoSource(t, `package sample

// @Unknown
type Broken struct{}
`)
	code, stdout, stderr := runModule(root, "verify", ".")
	if code != 1 ||
		stdout != "" ||
		!strings.Contains(
			stderr,
			"[spice.resolution.annotation-import]",
		) {
		t.Fatalf(
			"code=%d stdout=%q stderr=%q",
			code,
			stdout,
			stderr,
		)
	}
}

func TestVerifyJavaStructuredProfile(t *testing.T) {
	root := writeGoSource(t, `package sample

type First struct{}
type Second struct{}

func helper() {}
`)
	code, stdout, stderr := runModule(root, "verify", ".")
	if code != 0 || stdout == "" || stderr != "" {
		t.Fatalf(
			"default profile: code=%d stdout=%q stderr=%q",
			code,
			stdout,
			stderr,
		)
	}

	code, stdout, stderr = runModule(
		root,
		"verify",
		"--profile=java-structured",
		".",
	)
	if code != 1 || stdout != "" ||
		!strings.Contains(stderr, "[spice.style.file.one-primary-type]") ||
		!strings.Contains(stderr, "[spice.style.function.package-level]") ||
		!strings.Contains(stderr, "style profile error") {
		t.Fatalf(
			"java-structured: code=%d stdout=%q stderr=%q",
			code,
			stdout,
			stderr,
		)
	}
}

func TestVerifyUsesSharedSchemaTwoConfiguration(t *testing.T) {
	root := writeModule(t, map[string]string{"app/sample.go": `package sample

type First struct{}
type Second struct{}

func helper() {}
`})
	configurationPath, err := filepath.Abs(filepath.Join("..", "style", "testdata", "style.json"))
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(configurationPath)
	if err != nil {
		t.Fatal(err)
	}
	configured := styleConfigurationForApp(string(content))
	localPath := filepath.Join(root, "style.json")
	if err = os.WriteFile(localPath, []byte(configured), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runModule(root, "verify", "--style=style.json", ".")
	if code != 1 || stdout != "" ||
		!strings.Contains(stderr, "[spice.style.file.one-primary-type]") ||
		!strings.Contains(stderr, "[spice.style.function.package-level]") {
		t.Fatalf("configured: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	configuredOff := strings.ReplaceAll(configured, `"onePrimaryTypePerFile": "error"`, `"onePrimaryTypePerFile": "off"`)
	configuredOff = strings.ReplaceAll(configuredOff, `"packageFunctions": "error"`, `"packageFunctions": "off"`)
	if err = os.WriteFile(localPath, []byte(configuredOff), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr = runModule(root, "verify", "--style=style.json", ".")
	if code != 0 || stdout == "" || stderr != "" {
		t.Fatalf("configured off: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestVerifyStartsAnnotationToolOnHostUnderHostileAmbientTarget(t *testing.T) {
	root := writeModule(t, map[string]string{"app/doc.go": `// Package sample owns the test module.
// @Module
package sample
`})
	configurationPath, err := filepath.Abs(filepath.Join("..", "style", "testdata", "style.json"))
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(configurationPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(root, "style.json"),
		[]byte(styleConfigurationForApp(string(content))),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	hostileGoEnv := filepath.Join(t.TempDir(), "hostile-goenv")
	if err := os.WriteFile(hostileGoEnv, []byte(
		"GOOS=js\nGOARCH=wasm\nGOFLAGS=-tags=goenvambient\n"+
			"GOTOOLCHAIN=go1.99.0+auto\nGOPROXY=http://127.0.0.1:1\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CGO_ENABLED", "1")
	t.Setenv("GOAMD64", "v4")
	t.Setenv("GOARCH", "amd64")
	t.Setenv("GOAUTH", "netrc")
	t.Setenv("GOENV", hostileGoEnv)
	t.Setenv("GOEXPERIMENT", "ambientexperiment")
	t.Setenv("GOFIPS140", "latest")
	t.Setenv("GOFLAGS", "-tags=ambient")
	t.Setenv("GOOS", "plan9")
	t.Setenv("GOPROXY", "http://127.0.0.1:1")
	t.Setenv("GOSUMDB", "invalid.example")
	t.Setenv("GOTOOLCHAIN", "go1.99.0+auto")
	var stdout, stderr bytes.Buffer
	code := run(
		[]string{"verify", "--format=json", "--style=style.json", "."},
		&stdout,
		&stderr,
		load.Options{Dir: root, Env: os.Environ()},
		load.Load,
	)
	report := decodeDiagnosticReport(t, stdout.String())
	if code != 0 || stderr.String() != "" || !report.Success ||
		len(report.Diagnostics) != 0 {
		t.Fatalf(
			"code=%d report=%#v stderr=%q",
			code,
			report,
			stderr.String(),
		)
	}
}

func TestVerifyReportsSchemaMigrationAndClosedPolicyCodes(t *testing.T) {
	t.Parallel()
	configurationPath, err := filepath.Abs(filepath.Join("..", "style", "testdata", "style.json"))
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(configurationPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		mutate func(string) string
		code   string
		text   string
	}{
		{
			name: "schema one",
			mutate: func(value string) string {
				return strings.Replace(value, `"schemaVersion": 2`, `"schemaVersion": 1`, 1)
			},
			code: "spice.style.configuration.schema",
			text: "migrate",
		},
		{
			name: "unknown contribution exception",
			mutate: func(value string) string {
				return strings.Replace(value, `"contributionKind": "provider"`, `"contributionKind": "magic"`, 1)
			},
			code: "spice.style.configuration.schema",
			text: "contributionKind",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := writeGoSource(t, "package sample\n\ntype Sample struct{}\n")
			if err := os.WriteFile(filepath.Join(root, "style.json"), []byte(test.mutate(string(content))), 0o600); err != nil {
				t.Fatal(err)
			}
			code, stdout, stderr := runModule(root, "verify", "--format=json", "--style=style.json", ".")
			report := decodeDiagnosticReport(t, stdout)
			if code != 1 || stderr != "" || report.Success || len(report.Diagnostics) != 1 ||
				report.Diagnostics[0].Code != test.code || !strings.Contains(report.Diagnostics[0].Message, test.text) {
				t.Fatalf("code=%d report=%#v stderr=%q", code, report, stderr)
			}
		})
	}
}

func styleConfigurationForApp(content string) string {
	content = strings.ReplaceAll(
		content,
		`testdata/src/example.com/valid/internal/spicegen`,
		`app/internal/spicegen`,
	)
	return strings.ReplaceAll(content, `testdata/src`, `app`)
}

func TestVerifyRejectsInvalidFormattingOptions(t *testing.T) {
	t.Parallel()
	for _, arguments := range [][]string{
		{"verify", "--format"},
		{"verify", "--format=yaml"},
		{"verify", "--format=json", "--format=text"},
		{"verify", "--unknown"},
		{"verify", "--profile"},
		{"verify", "--profile=java"},
		{"verify", "--profile=java-structured", "--profile=java-structured"},
		{"verify", "--style"},
		{"verify", "--style=one.json", "--style=two.json"},
		{"verify", "--profile=java-structured", "--style=style.json"},
	} {
		var stdout, stderr bytes.Buffer
		code := Run(arguments, &stdout, &stderr)
		if code != 2 ||
			stdout.String() != "" ||
			stderr.String() == "" {
			t.Errorf(
				"%q: code=%d stdout=%q stderr=%q",
				arguments,
				code,
				stdout.String(),
				stderr.String(),
			)
		}
	}
}

func decodeDiagnosticReport(
	t *testing.T,
	content string,
) diagnostic.Report {
	t.Helper()
	var report diagnostic.Report
	if err := json.Unmarshal([]byte(content), &report); err != nil {
		t.Fatalf("decode report %q: %v", content, err)
	}
	if report.Schema != "spice.diagnostics/v1" {
		t.Fatalf("schema = %q", report.Schema)
	}
	return report
}
