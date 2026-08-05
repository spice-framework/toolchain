package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spice-framework/spice/compiler/annotationinstall"
	compilerservice "github.com/spice-framework/spice/compiler/service"
)

const lspFixtureTool = "example.com/spice-annotation-fixture/cmd/spice-annotations"

func TestAnnotationToolQuickFixRequiresPreviewBeforeHashGuardedApply(
	t *testing.T,
) {
	t.Parallel()
	root, source := writeAnnotationInstallModule(t)
	compiler, err := compilerservice.New(compilerservice.Config{})
	if err != nil {
		t.Fatalf("service.New() error = %v", err)
	}
	t.Cleanup(func() {
		if closeErr := compiler.Close(context.Background()); closeErr != nil {
			t.Errorf("Service.Close() error = %v", closeErr)
		}
	})
	var output bytes.Buffer
	server := &Server{
		writer: newRPCWriter(&output),
		workspaces: map[string]*workspace{
			pathKey(root): {
				root:    root,
				service: compiler,
			},
		},
		documents:        make(map[string]*document),
		toolPreviews:     make(map[string]annotationinstall.Preview),
		toolPreviewByKey: make(map[string]string),
	}
	definitions := server.catalogCompletionDefinitions(root, nil)
	annotationOffset := strings.Index(source, "@Factory")
	requestRange := protocolRangeAtOffsets(
		[]byte(source),
		annotationOffset,
		annotationOffset+len("@Factory"),
	)
	sourceDocument := document{
		path:    filepath.Join(root, "main.go"),
		root:    root,
		content: []byte(source),
	}
	metadata := metadataView{definitions: definitions}
	actions := server.annotationToolCodeActions(
		sourceDocument,
		requestRange,
		metadata,
	)
	if len(actions) != 1 ||
		actions[0].Command == nil ||
		actions[0].Command.Command != annotationToolPreviewCommand {
		t.Fatalf("initial annotation tool actions = %+v", actions)
	}
	original := readAnnotationInstallGoMod(t, root)
	executeToolAction(t, server, 1, actions[0])
	if current := readAnnotationInstallGoMod(t, root); current != original {
		t.Fatalf("preview changed go.mod:\n%s", current)
	}
	actions = server.annotationToolCodeActions(
		sourceDocument,
		requestRange,
		metadata,
	)
	if len(actions) != 2 ||
		actions[0].Command == nil ||
		actions[0].Command.Command != annotationToolApplyCommand {
		t.Fatalf("previewed annotation tool actions = %+v", actions)
	}
	executeToolAction(t, server, 2, actions[0])
	applied := readAnnotationInstallGoMod(t, root)
	if !strings.Contains(applied, "tool "+lspFixtureTool) ||
		!strings.Contains(output.String(), "Exact go.mod/go.sum diff") ||
		!strings.Contains(output.String(), `"status":"previewed"`) ||
		!strings.Contains(output.String(), `"status":"applied"`) {
		t.Fatalf(
			"applied go.mod/output:\n%s\n--- protocol ---\n%s",
			applied,
			output.String(),
		)
	}
}

func TestAnnotationToolPreviewRequestCanBeCanceledWhileRunning(
	t *testing.T,
) {
	t.Parallel()
	root, _ := writeAnnotationInstallModule(t)
	server, err := New(Config{NewService: testServiceFactory})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	started := make(chan struct{})
	server.previewTool = func(
		ctx context.Context,
		_ string,
		_ string,
		_ string,
		_ []string,
	) (annotationinstall.Preview, error) {
		close(started)
		<-ctx.Done()
		return annotationinstall.Preview{}, ctx.Err()
	}
	client := startTestClient(t, server)
	rootURI, err := fileURI(root)
	if err != nil {
		t.Fatalf("fileURI(root) error = %v", err)
	}
	client.send(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params":  map[string]any{"rootUri": rootURI},
	})
	_ = client.waitForID("1")
	client.send(map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "workspace/executeCommand",
		"params": map[string]any{
			"command": annotationToolPreviewCommand,
			"arguments": []any{annotationToolCommandArguments{
				Root:    root,
				Tool:    lspFixtureTool,
				Version: "v0.0.0",
			}},
		},
	})
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for annotation tool preview")
	}
	client.notify("$/cancelRequest", map[string]any{"id": 2})
	response := client.waitForID("2")
	if response.Error == nil ||
		response.Error.Code != requestCancelledCode {
		t.Fatalf("canceled preview response = %+v", response)
	}
	client.send(map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "shutdown",
	})
	_ = client.waitForID("3")
	client.notify("exit", nil)
	client.closeInput()
	if err := client.wait(); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func executeToolAction(
	t *testing.T,
	server *Server,
	id int,
	action protocolCodeAction,
) {
	t.Helper()
	params, err := json.Marshal(executeCommandParams{
		Command: action.Command.Command,
		Arguments: []json.RawMessage{
			marshalToolArgument(t, action.Command.Arguments[0]),
		},
	})
	if err != nil {
		t.Fatalf("Marshal(execute command) error = %v", err)
	}
	if err := server.executeCommand(rpcMessage{
		JSONRPC: "2.0",
		ID:      json.RawMessage([]byte{byte('0' + id)}),
		Method:  "workspace/executeCommand",
		Params:  params,
	}); err != nil {
		t.Fatalf("executeCommand() error = %v", err)
	}
}

func marshalToolArgument(t *testing.T, value any) json.RawMessage {
	t.Helper()
	content, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("Marshal(tool argument) error = %v", err)
	}
	return content
}

func writeAnnotationInstallModule(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	fixture := filepath.Join(repository, "testdata", "annotationfixture")
	goMod := "module example.com/lsp-install-app\n\ngo 1.26.0\n\n" +
		"require example.com/spice-annotation-fixture v0.0.0\n\n" +
		"replace example.com/spice-annotation-fixture => " +
		filepath.ToSlash(fixture) + "\n\n" +
		"replace github.com/spice-framework/spice => " +
		filepath.ToSlash(repository) + "\n"
	source := `package main

// @import { Factory } from "example.com/spice-annotation-fixture/annotation/wiring"

// @Factory
func provideValue() int { return 1 }
`
	for name, content := range map[string]string{
		"go.mod":  goMod,
		"main.go": source,
	} {
		if err := os.WriteFile(
			filepath.Join(root, name),
			[]byte(content),
			0o600,
		); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}
	return root, source
}

func readAnnotationInstallGoMod(t *testing.T, root string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatalf("ReadFile(go.mod) error = %v", err)
	}
	return string(content)
}
