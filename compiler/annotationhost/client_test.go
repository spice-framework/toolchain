package annotationhost

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spice-framework/spice/annotation"
	"github.com/spice-framework/spice/annotation/sdk"
	"github.com/spice-framework/spice/annotation/sdk/protocol"
	"github.com/spice-framework/toolchain/internal/testsupport"
)

const fixtureTool = "example.com/annotationfixture/cmd/annotations"

var fixtureDescriptor = sdk.Symbol{
	Package: "example.com/annotationfixture/annotation",
	Name:    "Echo",
}

func TestClientLaunchesAuthorizedOfflineToolAndAnalyzes(t *testing.T) {
	root := writeToolFixture(t)
	client, err := Start(context.Background(), Config{
		Root:         root,
		ToolPath:     fixtureTool,
		SpiceVersion: "test",
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if got := client.Provenance(); got.Path != fixtureTool ||
		got.Module.Path != "example.com/annotationfixture" {
		t.Fatalf("Provenance() = %#v", got)
	}
	handlers := client.Handlers()
	if len(handlers) != 1 ||
		handlers[0].Descriptor != fixtureDescriptor {
		t.Fatalf("Handlers() = %#v", handlers)
	}
	packages := client.DescriptorPackages()
	if len(packages) != 1 ||
		packages[0] != "example.com/annotationfixture/annotation" {
		t.Fatalf("DescriptorPackages() = %#v", packages)
	}
	result, err := client.Analyze(context.Background(), protocol.AnalyzeParams{
		Descriptor: fixtureDescriptor,
		Invocation: protocol.Invocation{
			DescriptorPackage: fixtureDescriptor.Package,
			DescriptorSymbol:  fixtureDescriptor.Name,
			CanonicalName:     "fixture.Echo",
		},
	})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if len(result.Contributions) != 1 ||
		result.Contributions[0].Kind != "fixture.echo" {
		t.Fatalf("Analyze() = %#v", result)
	}
	closeCtx, cancel := context.WithTimeout(
		context.Background(),
		5*time.Second,
	)
	defer cancel()
	if err := client.Close(closeCtx); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := client.Close(closeCtx); err != nil {
		t.Fatalf("Close(second) error = %v", err)
	}
}

func TestClientUsesHostToolchainUnderHostileTargetEnvironment(t *testing.T) {
	tests := []struct {
		name    string
		mode    string
		wantErr string
	}{
		{name: "host identity", mode: "host-environment"},
		{name: "real tool failure", mode: "crash", wantErr: "fixture crash"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := writeToolFixture(t)
			client, err := Start(t.Context(), Config{
				Root:         root,
				ToolPath:     fixtureTool,
				Environment:  hostileToolEnvironment(test.mode),
				StartTimeout: 15 * time.Second,
			})
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("Start() error = %v, want %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Start() error = %v", err)
			}
			if err := client.Close(t.Context()); err != nil {
				t.Fatalf("Close() error = %v", err)
			}
		})
	}
}

func hostileToolEnvironment(mode string) []string {
	result := os.Environ()
	for _, setting := range []struct {
		name  string
		value string
	}{
		{name: "CGO_ENABLED", value: "1"},
		{name: "GOAMD64", value: "v4"},
		{name: "GOARCH", value: "amd64"},
		{name: "GOENV", value: filepath.Join("missing", "goenv")},
		{name: "GOEXPERIMENT", value: "definitely-invalid"},
		{name: "GOFIPS140", value: "latest"},
		{name: "GOFLAGS", value: "-tags=ambient"},
		{name: "GOOS", value: "plan9"},
		{name: "GOPROXY", value: "https://proxy.invalid"},
		{name: "GOSUMDB", value: "sum.invalid"},
		{name: "GOTOOLCHAIN", value: "auto"},
		{name: "SPICE_FIXTURE_MODE", value: mode},
	} {
		result = replaceEnvironmentValue(result, setting.name, setting.value)
	}
	return result
}

func TestValidateDescriptorPackagesRejectsMissingDuplicateAndForeign(
	t *testing.T,
) {
	t.Parallel()
	for _, values := range [][]string{
		nil,
		{
			"example.com/tool/annotation",
			"example.com/tool/annotation",
		},
		{"example.net/foreign/annotation"},
		{" example.com/tool/annotation"},
	} {
		if _, err := validateDescriptorPackages(
			values,
			"example.com/tool",
			"example.com/tool/cmd/annotations",
		); err == nil {
			t.Fatalf(
				"validateDescriptorPackages(%q) error = nil",
				values,
			)
		}
	}
}

func TestValidateDescriptorPackagesAllowsOnlyOfficialCrossModuleDescriptors(
	t *testing.T,
) {
	t.Parallel()
	packages, err := validateDescriptorPackages(
		[]string{"github.com/spice-framework/spice/annotation/core"},
		"github.com/spice-framework/toolchain",
		"github.com/spice-framework/toolchain/cmd/spice-annotation-core",
	)
	if err != nil || len(packages) != 1 {
		t.Fatalf("validateDescriptorPackages() = %v, %v", packages, err)
	}
	if _, err := validateDescriptorPackages(
		[]string{"github.com/spice-framework/spice/annotation/core"},
		"example.com/toolchain",
		"example.com/toolchain/cmd/annotations",
	); err == nil {
		t.Fatal("third-party cross-module descriptor error = nil")
	}
}

func TestValidateDescriptorRejectsPackageNotDeclaredByTool(t *testing.T) {
	t.Parallel()
	client := &Client{
		provenance: PackageIdentity{
			Path: fixtureTool,
			Module: ModuleIdentity{
				Path:    "example.com/annotationfixture",
				Version: "v1.0.0",
			},
		},
		descriptorPackages: []string{
			"example.com/annotationfixture/annotation",
		},
	}
	err := client.ValidateDescriptor(
		"example.com/annotationfixture/other",
		"Undeclared",
		sdk.Definition{
			Name: "Undeclared",
		},
		annotation.ModuleProvenance{
			Path:    "example.com/annotationfixture",
			Version: "v1.0.0",
		},
	)
	if err == nil || !strings.Contains(
		err.Error(),
		"package \"example.com/annotationfixture/other\" is not declared",
	) {
		t.Fatalf("ValidateDescriptor() error = %v", err)
	}
}

func TestClientRejectsUndeclaredToolBeforeLaunch(t *testing.T) {
	root := writeToolFixture(t)
	_, err := Start(context.Background(), Config{
		Root:     root,
		ToolPath: "example.com/annotationfixture/cmd/other",
	})
	if err == nil || !strings.Contains(err.Error(), "not authorized") {
		t.Fatalf("Start() error = %v", err)
	}
}

func TestClientFailsClosedOnIdentityAndProtocolFailures(t *testing.T) {
	tests := []struct {
		name    string
		mode    string
		message string
	}{
		{
			name:    "identity",
			mode:    "identity",
			message: "identity mismatch",
		},
		{
			name:    "stdout contamination",
			mode:    "contaminate",
			message: "unsupported header",
		},
		{
			name:    "crash",
			mode:    "crash",
			message: "fixture crash",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := writeToolFixture(t)
			_, err := Start(context.Background(), Config{
				Root:     root,
				ToolPath: fixtureTool,
				Environment: append(
					os.Environ(),
					"SPICE_FIXTURE_MODE="+test.mode,
				),
				StartTimeout: 15 * time.Second,
				StderrBytes:  64,
			})
			if err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("Start() error = %v, want %q", err, test.message)
			}
		})
	}
}

func TestClientCancelsHungAnalysisAndDoesNotReplay(t *testing.T) {
	root := writeToolFixture(t)
	client, err := Start(context.Background(), Config{
		Root:     root,
		ToolPath: fixtureTool,
		Environment: append(
			os.Environ(),
			"SPICE_FIXTURE_MODE=hang-analyze",
		),
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	client.config.CallTimeout = 200 * time.Millisecond
	_, err = client.Analyze(context.Background(), protocol.AnalyzeParams{
		Descriptor: fixtureDescriptor,
	})
	if err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("Analyze() error = %v", err)
	}
	_, err = client.Analyze(context.Background(), protocol.AnalyzeParams{
		Descriptor: fixtureDescriptor,
	})
	if err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("Analyze(after timeout) error = %v", err)
	}
}

func writeToolFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeToolFile(t, root, "go.mod", `module example.com/annotationfixture

go 1.26.0

tool example.com/annotationfixture/cmd/annotations

require github.com/spice-framework/spice v0.0.0

replace github.com/spice-framework/spice => `+filepath.ToSlash(testsupport.CoreDirectory(t))+"\n")
	writeToolFile(t, root, "cmd/annotations/main.go", toolFixtureSource)
	return root
}

func writeToolFile(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}

const toolFixtureSource = `package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/spice-framework/spice/annotation/sdk"
	"github.com/spice-framework/spice/annotation/sdk/protocol"
)

func main() {
	if len(os.Args) != 2 || os.Args[1] != "--spice-stdio" {
		os.Exit(2)
	}
	mode := os.Getenv("SPICE_FIXTURE_MODE")
	if mode == "host-environment" &&
		(runtime.GOOS != os.Getenv("GOOS") ||
			runtime.GOARCH != os.Getenv("GOARCH") ||
			os.Getenv("CGO_ENABLED") != "0" ||
			os.Getenv("GOENV") != "off" ||
			os.Getenv("GOFIPS140") != "off" ||
			os.Getenv("GOTOOLCHAIN") != "local" ||
			os.Getenv("GOPROXY") != "off" ||
			os.Getenv("GOSUMDB") != "off" ||
			strings.Contains(os.Getenv("GOFLAGS"), "ambient")) {
		fmt.Fprintf(os.Stderr, "host environment mismatch: runtime=%s/%s env=%s/%s cgo=%s goenv=%s fips=%s toolchain=%s proxy=%s sumdb=%s flags=%s",
			runtime.GOOS, runtime.GOARCH, os.Getenv("GOOS"), os.Getenv("GOARCH"),
			os.Getenv("CGO_ENABLED"), os.Getenv("GOENV"), os.Getenv("GOFIPS140"), os.Getenv("GOTOOLCHAIN"),
			os.Getenv("GOPROXY"), os.Getenv("GOSUMDB"), os.Getenv("GOFLAGS"))
		os.Exit(4)
	}
	if mode == "contaminate" {
		fmt.Print("unexpected stdout\n")
	}
	reader := bufio.NewReader(os.Stdin)
	for {
		var request protocol.Request
		if err := protocol.ReadMessage(reader, &request); err != nil {
			return
		}
		if mode == "crash" {
			fmt.Fprint(os.Stderr, "fixture crash with bounded diagnostic output")
			os.Exit(3)
		}
		var result any
		switch request.Method {
		case "initialize":
			modulePath := "example.com/annotationfixture"
			if mode == "identity" {
				modulePath = "example.com/wrong"
			}
			result = protocol.InitializeResult{
				Protocol: sdk.ProtocolV1Alpha2,
				ToolPath: "example.com/annotationfixture/cmd/annotations",
				ModulePath: modulePath,
			}
		case "describe":
			result = protocol.DescribeResult{
				DescriptorPackages: []string{
					"example.com/annotationfixture/annotation",
				},
				Handlers: []protocol.Handler{{
				Descriptor: sdk.Symbol{
					Package: "example.com/annotationfixture/annotation",
					Name: "Echo",
				},
				Capabilities: []string{"diagnostics"},
				}},
			}
		case "analyze":
			if mode == "hang-analyze" {
				time.Sleep(time.Hour)
			}
			value, _ := json.Marshal(map[string]bool{"accepted": true})
			result = protocol.AnalyzeResult{Contributions: []protocol.Contribution{{
				Kind: "fixture.echo",
				Value: value,
			}}}
		case "shutdown":
			result = struct{}{}
		default:
			writeResponse(request.ID, nil, &protocol.ResponseError{
				Code: -32601,
				Message: "method not found",
			})
			continue
		}
		writeResponse(request.ID, result, nil)
		if request.Method == "shutdown" {
			return
		}
	}
}

func writeResponse(id uint64, result any, responseError *protocol.ResponseError) {
	content, _ := json.Marshal(result)
	_ = protocol.WriteMessage(os.Stdout, protocol.Response{
		JSONRPC: "2.0",
		ID: id,
		Result: content,
		Error: responseError,
	})
}
`
