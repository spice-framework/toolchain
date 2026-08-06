package cli

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/spice-framework/toolchain/compiler/load"
	compilerstarter "github.com/spice-framework/toolchain/compiler/starter"
	"github.com/spice-framework/toolchain/internal/moduleenv"
)

func TestDecodeModuleVersions(t *testing.T) {
	input := strings.NewReader(`{
		"Path": "example.com/application",
		"Main": true
	}
	{
		"Path": "example.com/client",
		"Version": "v1.0.0",
		"Replace": {
			"Path": "example.com/client",
			"Version": "v1.2.3"
		}
	}`)
	got, err := decodeModuleVersions(input)
	if err != nil {
		t.Fatalf("decodeModuleVersions() error = %v", err)
	}
	want := []compilerstarter.ModuleVersion{
		{Path: "example.com/application", Main: true},
		{
			Path:               "example.com/client",
			Version:            "v1.0.0",
			ReplacementPath:    "example.com/client",
			ReplacementVersion: "v1.2.3",
		},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("decodeModuleVersions() = %#v, want %#v", got, want)
	}
}

func TestDecodeModuleVersionsRejectsMalformedGraph(t *testing.T) {
	_, err := decodeModuleVersions(strings.NewReader(`{"Path":`))
	if err == nil {
		t.Fatal("decodeModuleVersions() error = nil")
	}
}

func TestDecodeModuleVersionsEnforcesEntryLimit(t *testing.T) {
	input := strings.NewReader(
		strings.Repeat("{\"Path\":\"example.com/module\"}\n", maxModuleGraphEntries+1),
	)
	_, err := decodeModuleVersions(input)
	if err == nil || !strings.Contains(err.Error(), "safety limit") {
		t.Fatalf("decodeModuleVersions() error = %v", err)
	}
}

func TestModuleVersionsFromVendorPreservesWorkspaceAndReplacementProvenance(t *testing.T) {
	t.Parallel()
	got, err := moduleVersionsFromVendor(
		[]moduleenv.WorkspaceModule{
			{Path: "example.com/plugin", Root: "plugin"},
			{Path: "example.com/application", Root: "application"},
		},
		[]moduleenv.VendoredModule{
			{
				Path: "example.com/local", Version: "v0.0.0",
				ReplacementPath: "../local", LocalReplacement: true,
			},
			{
				Path: "example.com/remote", Version: "v1.2.3",
				ReplacementPath: "example.com/fork", ReplacementVersion: "v1.4.0",
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []compilerstarter.ModuleVersion{
		{Path: "example.com/application", Main: true},
		{Path: "example.com/plugin", Main: true},
		{
			Path: "example.com/local", Version: "v0.0.0",
			ReplacementPath: "../local",
		},
		{
			Path: "example.com/remote", Version: "v1.2.3",
			ReplacementPath: "example.com/fork", ReplacementVersion: "v1.4.0",
		},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("moduleVersionsFromVendor() = %#v, want %#v", got, want)
	}
}

func TestModuleVersionsFromVendorRejectsDuplicateModulePath(t *testing.T) {
	t.Parallel()
	_, err := moduleVersionsFromVendor(
		[]moduleenv.WorkspaceModule{{Path: "example.com/duplicate"}},
		[]moduleenv.VendoredModule{{Path: "example.com/duplicate", Version: "v1.0.0"}},
	)
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("moduleVersionsFromVendor() error = %v", err)
	}
}

func TestLoadModuleVersionsHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := loadModuleVersions(ctx, load.Options{Dir: t.TempDir()})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("loadModuleVersions() error = %v, want context.Canceled", err)
	}
}

func TestModuleGraphEnvironmentIsOfflineAndImmutable(t *testing.T) {
	environment := []string{
		"PATH=fixture",
		"GOPROXY=https://proxy.example",
		"gosumdb=sum.example",
	}
	got := moduleGraphEnvironment(environment)
	want := []string{
		"PATH=fixture",
		"GOPROXY=off",
		"GOSUMDB=off",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("moduleGraphEnvironment() = %v, want %v", got, want)
	}
	if environment[1] != "GOPROXY=https://proxy.example" {
		t.Fatalf("moduleGraphEnvironment() mutated input: %v", environment)
	}
}

func TestBoundedBufferTruncatesDiagnosticOutput(t *testing.T) {
	buffer := newBoundedBuffer(4)
	if written, err := buffer.Write([]byte("123456")); written != 6 || err != nil {
		t.Fatalf("Write() = %d, %v", written, err)
	}
	if got := buffer.String(); got != "1234\n... output truncated" {
		t.Fatalf("String() = %q", got)
	}
}
