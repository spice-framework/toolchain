package annotationhost

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/StevenBuglione/spice/annotation/sdk/protocol"
)

const fixtureTool = "example.com/annotationfixture/cmd/annotations"

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
	if len(handlers) != 1 || handlers[0].ID != "fixture/echo" {
		t.Fatalf("Handlers() = %#v", handlers)
	}
	result, err := client.Analyze(context.Background(), protocol.AnalyzeParams{
		Handler: "fixture/echo",
		Invocation: protocol.Invocation{
			CanonicalName: "fixture.Echo",
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
		Handler: "fixture/echo",
	})
	if err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("Analyze() error = %v", err)
	}
	_, err = client.Analyze(context.Background(), protocol.AnalyzeParams{
		Handler: "fixture/echo",
	})
	if err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("Analyze(after timeout) error = %v", err)
	}
}

func writeToolFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	writeToolFile(t, root, "go.mod", `module example.com/annotationfixture

go 1.26.0

tool example.com/annotationfixture/cmd/annotations

require github.com/StevenBuglione/spice v0.0.0

replace github.com/StevenBuglione/spice => `+filepath.ToSlash(repository)+"\n")
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
	"time"

	"github.com/StevenBuglione/spice/annotation/sdk"
	"github.com/StevenBuglione/spice/annotation/sdk/protocol"
)

func main() {
	if len(os.Args) != 2 || os.Args[1] != "--spice-stdio" {
		os.Exit(2)
	}
	mode := os.Getenv("SPICE_FIXTURE_MODE")
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
				Protocol: sdk.ProtocolV1Alpha1,
				ToolPath: "example.com/annotationfixture/cmd/annotations",
				ModulePath: modulePath,
			}
		case "describe":
			result = protocol.DescribeResult{Handlers: []protocol.Handler{{
				ID: "fixture/echo",
				Capabilities: []string{"diagnostics"},
				Source: sdk.Symbol{
					Package: "example.com/annotationfixture/internal/handler",
					Name: "Echo",
				},
			}}}
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
