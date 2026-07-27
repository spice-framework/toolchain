package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAnnotationsListAndDoctorAreReadOnlyAndFailClosed(t *testing.T) {
	root := t.TempDir()
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	writeAnnotationCommandFile(t, root, "go.mod", `module example.com/fixture

go 1.26.0

require github.com/StevenBuglione/spice v0.0.0

replace github.com/StevenBuglione/spice => `+filepath.ToSlash(repository)+"\n")
	writeAnnotationCommandFile(t, root, "app/app.go", `package app

// @spice.import { Echo } from "example.com/fixture/annotations"

// @Echo
type Value struct{}
`)
	writeAnnotationCommandFile(
		t,
		root,
		"annotations/echo.go",
		`package annotations

import "github.com/StevenBuglione/spice/annotation/sdk"

// Echo documents the fixture annotation.
func Echo() sdk.Definition {
	return sdk.Definition{
		Name: "fixture.Echo",
		Summary: "Fixture annotation.",
		Targets: []sdk.Target{sdk.TargetType},
		Examples: []sdk.Example{{
			Title: "Echo",
			Code: "// @Echo",
		}},
		Compatibility: sdk.Compatibility{
			Since: "0.1.0",
			MinimumSpice: "0.1.0",
		},
		Implementation: sdk.Implementation{
			Tool: "example.com/fixture/cmd/annotations",
			Handler: "fixture/echo",
			Protocol: sdk.ProtocolV1Alpha1,
			Source: sdk.Symbol{
				Package: "example.com/fixture/internal/handler",
				Name: "Echo",
			},
		},
	}
}
`,
	)
	code, stdout, stderr := runModule(root, "annotations", "list", "./app")
	if code != 0 ||
		!strings.Contains(stdout, "fixture.Echo") ||
		!strings.Contains(stdout, "unauthorized") ||
		stderr != "" {
		t.Fatalf(
			"annotations list code=%d stdout=%q stderr=%q",
			code,
			stdout,
			stderr,
		)
	}
	code, stdout, stderr = runModule(
		root,
		"annotations",
		"doctor",
		"./app",
	)
	if code != 1 ||
		stdout != "" ||
		!strings.Contains(stderr, "not authorized") ||
		!strings.Contains(stderr, "go get -tool") {
		t.Fatalf(
			"annotations doctor code=%d stdout=%q stderr=%q",
			code,
			stdout,
			stderr,
		)
	}
	if _, err := os.Stat(filepath.Join(root, "go.sum")); !os.IsNotExist(err) {
		t.Fatalf("read-only commands created go.sum: %v", err)
	}
}

func writeAnnotationCommandFile(
	t *testing.T,
	root string,
	name string,
	content string,
) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}
