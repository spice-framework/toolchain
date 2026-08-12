package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/spice-framework/toolchain/internal/identity"
	"github.com/spice-framework/toolchain/internal/testsupport"
)

func TestNormalizeArgumentsMapsDocumentedFormats(t *testing.T) {
	t.Parallel()
	input := []string{"--format=json", "--config=.spice/style.json", "./..."}
	wanted := []string{"verify", "--format=json", "--style=.spice/style.json", "./..."}
	if got := normalizeArguments(input); !slices.Equal(got, wanted) {
		t.Fatalf("normalizeArguments() = %v, want %v", got, wanted)
	}
}

func TestNormalizeArgumentsMapsSeparatedConfiguration(t *testing.T) {
	t.Parallel()
	input := []string{"--config", "profile.json", "./..."}
	wanted := []string{"verify", "--style=profile.json", "./..."}
	if got := normalizeArguments(input); !slices.Equal(got, wanted) {
		t.Fatalf("normalizeArguments() = %v, want %v", got, wanted)
	}
}

func TestNormalizeArgumentsPreservesTextAndReportsMissingConfiguration(t *testing.T) {
	t.Parallel()
	input := []string{"--format=text", "-config", "./..."}
	wanted := []string{"verify", "--format=text", "--style=./..."}
	if got := normalizeArguments(input); !slices.Equal(got, wanted) {
		t.Fatalf("normalizeArguments() = %v, want %v", got, wanted)
	}
	missing := []string{"--config"}
	wantedMissing := []string{"verify", "--style"}
	if got := normalizeArguments(missing); !slices.Equal(got, wantedMissing) {
		t.Fatalf("normalizeArguments(missing) = %v, want %v", got, wantedMissing)
	}
}

func TestNormalizeArgumentsDefaultsToSchemaTwoConfiguration(t *testing.T) {
	t.Parallel()
	wanted := []string{"verify", "./...", "--style=.spice/style.json"}
	if got := normalizeArguments([]string{"./..."}); !slices.Equal(got, wanted) {
		t.Fatalf("normalizeArguments(default) = %v, want %v", got, wanted)
	}
}

func TestRunStartsAnnotationToolOnHostUnderHostileAmbientTarget(t *testing.T) {
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if mkdirErr := os.MkdirAll(filepath.Join(root, "app"), 0o750); mkdirErr != nil {
		t.Fatal(mkdirErr)
	}
	goMod := "module example.com/spicestylehost\n\ngo 1.26.0\n\n" +
		"tool " + identity.AnnotationTool + "\n\n" +
		"require (\n\t" + identity.CoreModule + " " + identity.CoreVersion + "\n" +
		"\t" + identity.ToolchainModule + " v0.0.0\n)\n\n" +
		"replace " + identity.CoreModule + " => " +
		filepath.ToSlash(testsupport.CoreDirectory(t)) + "\n\n" +
		"replace " + identity.ToolchainModule + " => " +
		filepath.ToSlash(repository) + "\n"
	stylePath := filepath.Join(repository, "internal", "style", "testdata", "style.json")
	style, err := os.ReadFile(stylePath)
	if err != nil {
		t.Fatal(err)
	}
	style = []byte(strings.ReplaceAll(
		strings.ReplaceAll(
			string(style),
			`testdata/src/example.com/valid/internal/spicegen`,
			`app/internal/spicegen`,
		),
		`testdata/src`,
		`app`,
	))
	for name, content := range map[string][]byte{
		"go.mod":     []byte(goMod),
		"style.json": style,
		filepath.Join("app", "doc.go"): []byte(`// @import { Module } from "github.com/spice-framework/spice/annotation/modulith"

// Package sample owns the test module.
// @Module
package sample
`),
	} {
		if err := os.WriteFile(filepath.Join(root, name), content, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(root)
	hostileGoEnv := filepath.Join(root, "hostile-goenv")
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
		[]string{"--format=json", "--config=style.json", "."},
		&stdout,
		&stderr,
	)
	if code != 0 || stderr.String() != "" ||
		!strings.Contains(stdout.String(), `"success": true`) {
		t.Fatalf(
			"run() code=%d stdout=%q stderr=%q",
			code,
			stdout.String(),
			stderr.String(),
		)
	}
}
