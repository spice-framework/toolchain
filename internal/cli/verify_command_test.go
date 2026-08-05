package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spice-framework/spice/compiler/diagnostic"
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

func TestVerifyRejectsInvalidFormattingOptions(t *testing.T) {
	t.Parallel()
	for _, arguments := range [][]string{
		{"verify", "--format"},
		{"verify", "--format=yaml"},
		{"verify", "--format=json", "--format=text"},
		{"verify", "--unknown"},
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
