package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spice-framework/toolchain/compiler/load"
	compilerservice "github.com/spice-framework/toolchain/compiler/service"
	"github.com/spice-framework/toolchain/internal/identity"
	"github.com/spice-framework/toolchain/internal/testsupport"
)

// The end-to-end client starts the complete typed compiler pipeline. Under the
// repository-wide race pass it competes with every other package, so the
// failure bound must tolerate scheduler pressure without changing fast runs.
const testClientTimeout = 45 * time.Second

func TestServerDeveloperWorkflowUsesVersionedCompilerResults(t *testing.T) {
	root, mainPath, original := writeLSPModule(t)
	writeAnnotationReference(t, root)
	server, err := New(Config{
		NewService: func(string) (*compilerservice.Service, error) {
			return compilerservice.New(compilerservice.Config{})
		},
		AnalysisDelay: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	client := startTestClient(t, server)
	rootURI, err := fileURI(root)
	if err != nil {
		t.Fatalf("fileURI(root) error = %v", err)
	}
	mainURI, err := fileURI(mainPath)
	if err != nil {
		t.Fatalf("fileURI(main.go) error = %v", err)
	}

	client.send(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"workspaceFolders": []map[string]any{{
				"uri":  rootURI,
				"name": "fixture",
			}},
		},
	})
	initialize := client.waitForID("1")
	if initialize.Error != nil ||
		!strings.Contains(string(initialize.Result), "completionProvider") ||
		!strings.Contains(string(initialize.Result), "definitionProvider") ||
		!strings.Contains(string(initialize.Result), "documentLinkProvider") ||
		!strings.Contains(string(initialize.Result), "semanticTokensProvider") {
		t.Fatalf("initialize response = %+v", initialize)
	}

	invalid := strings.Replace(
		original,
		"// @Application",
		"// @Application\n// @Enab",
		1,
	)
	client.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri":        mainURI,
			"languageId": "go",
			"version":    1,
			"text":       invalid,
		},
	})
	first := client.waitForDiagnostics(mainURI, 1)
	invalidLine, invalidCharacter := sourcePosition(invalid, "@Enab")
	if len(first.Diagnostics) == 0 ||
		first.Diagnostics[0].Code != "spice.resolution.annotation-import" ||
		first.Diagnostics[0].Range.Start.Line != invalidLine {
		t.Fatalf("version 1 diagnostics = %+v", first.Diagnostics)
	}
	client.send(map[string]any{
		"jsonrpc": "2.0",
		"id":      20,
		"method":  "textDocument/semanticTokens/full",
		"params": map[string]any{
			"textDocument": map[string]any{"uri": mainURI},
		},
	})
	semanticResponse := client.waitForID("20")
	var semanticResult semanticTokensResult
	if err := json.Unmarshal(
		semanticResponse.Result,
		&semanticResult,
	); err != nil {
		t.Fatalf("Unmarshal(semantic tokens) error = %v", err)
	}
	if len(semanticResult.Data) < 10 {
		t.Fatalf("semantic token data = %v", semanticResult.Data)
	}

	client.send(map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "textDocument/completion",
		"params": map[string]any{
			"textDocument": map[string]any{"uri": mainURI},
			"position": map[string]any{
				"line":      invalidLine,
				"character": invalidCharacter + len("@Enab"),
			},
		},
	})
	completion := client.waitForID("2")
	var completionResult completionList
	if err := json.Unmarshal(completion.Result, &completionResult); err != nil {
		t.Fatalf("Unmarshal(completion) error = %v", err)
	}
	foundManagement := false
	for _, item := range completionResult.Items {
		if item.Label == "@Enable" &&
			strings.HasPrefix(item.TextEdit.NewText, "@Enable") &&
			len(item.AdditionalEdits) == 1 &&
			strings.Contains(
				item.AdditionalEdits[0].NewText,
				`// @import { Enable } from "github.com/spice-framework/spice/annotation/management"`,
			) {
			foundManagement = true
		}
	}
	if !foundManagement {
		t.Fatalf("completion items = %+v", completionResult.Items)
	}

	validWithConfigurationKey := strings.Replace(
		original,
		"// @Application",
		"var property = \"orders.limit\"\n\n// @Application",
		1,
	)
	client.change(mainURI, 2, validWithConfigurationKey)
	second := client.waitForDiagnostics(mainURI, 2)
	if second.Diagnostics == nil {
		t.Fatal("version 2 diagnostics must be an empty JSON array, not null")
	}
	if len(second.Diagnostics) != 0 {
		t.Fatalf("version 2 diagnostics = %+v", second.Diagnostics)
	}

	applicationLine, applicationCharacter := sourcePosition(
		validWithConfigurationKey,
		"Application",
	)
	client.send(map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "textDocument/hover",
		"params": map[string]any{
			"textDocument": map[string]any{"uri": mainURI},
			"position": map[string]any{
				"line":      applicationLine,
				"character": applicationCharacter,
			},
		},
	})
	hover := client.waitForID("3")
	if !strings.Contains(string(hover.Result), "`@core.Application`") {
		t.Fatalf("hover result = %s", hover.Result)
	}
	client.send(map[string]any{
		"jsonrpc": "2.0",
		"id":      30,
		"method":  "textDocument/definition",
		"params": map[string]any{
			"textDocument": map[string]any{"uri": mainURI},
			"position": map[string]any{
				"line":      applicationLine,
				"character": applicationCharacter,
			},
		},
	})
	definitionResponse := client.waitForID("30")
	var definitionLinks []protocolLocationLink
	if err := json.Unmarshal(
		definitionResponse.Result,
		&definitionLinks,
	); err != nil {
		t.Fatalf("Unmarshal(definition links) error = %v", err)
	}
	if len(definitionLinks) != 1 ||
		!strings.HasSuffix(
			definitionLinks[0].TargetURI,
			"/annotation/core/application.go",
		) ||
		definitionLinks[0].OriginSelectionRange.Start.Line != applicationLine {
		t.Fatalf("definition links = %+v", definitionLinks)
	}

	client.send(map[string]any{
		"jsonrpc": "2.0",
		"id":      31,
		"method":  "textDocument/documentLink",
		"params": map[string]any{
			"textDocument": map[string]any{"uri": mainURI},
		},
	})
	documentLinkResponse := client.waitForID("31")
	var documentLinks []protocolDocumentLink
	if err := json.Unmarshal(
		documentLinkResponse.Result,
		&documentLinks,
	); err != nil {
		t.Fatalf("Unmarshal(document links) error = %v", err)
	}
	if len(documentLinks) == 0 ||
		!strings.Contains(
			documentLinks[0].Target,
			"/annotation/core/application.go#",
		) ||
		!strings.Contains(
			documentLinks[0].Tooltip,
			"annotation/core.Application",
		) {
		t.Fatalf("document links = %+v", documentLinks)
	}

	propertyLine, propertyCharacter := sourcePosition(
		validWithConfigurationKey,
		"orders.",
	)
	propertyCharacter += len("orders.")
	client.send(map[string]any{
		"jsonrpc": "2.0",
		"id":      4,
		"method":  "textDocument/completion",
		"params": map[string]any{
			"textDocument": map[string]any{"uri": mainURI},
			"position": map[string]any{
				"line":      propertyLine,
				"character": propertyCharacter,
			},
		},
	})
	propertyCompletion := client.waitForID("4")
	var propertyResult completionList
	if err := json.Unmarshal(
		propertyCompletion.Result,
		&propertyResult,
	); err != nil {
		t.Fatalf("Unmarshal(property completion) error = %v", err)
	}
	foundProperty := false
	for _, item := range propertyResult.Items {
		if item.Label == "orders.limit" {
			foundProperty = true
		}
	}
	if !foundProperty {
		t.Fatalf("property completion items = %+v", propertyResult.Items)
	}
	_, propertyHoverCharacter := sourcePosition(
		validWithConfigurationKey,
		"orders.limit",
	)
	propertyHoverCharacter += len("orders.")
	client.send(map[string]any{
		"jsonrpc": "2.0",
		"id":      40,
		"method":  "textDocument/hover",
		"params": map[string]any{
			"textDocument": map[string]any{"uri": mainURI},
			"position": map[string]any{
				"line":      propertyLine,
				"character": propertyHoverCharacter,
			},
		},
	})
	propertyHover := client.waitForID("40")
	if !strings.Contains(
		string(propertyHover.Result),
		"Default: `100`",
	) {
		t.Fatalf("configuration hover result = %s", propertyHover.Result)
	}

	raw := strings.Replace(
		validWithConfigurationKey,
		"// @Application",
		"@Application",
		1,
	)
	client.change(mainURI, 3, raw)
	third := client.waitForDiagnostics(mainURI, 3)
	if len(third.Diagnostics) != 1 ||
		third.Diagnostics[0].Code != "spice.source.annotation-comment" ||
		!strings.Contains(
			third.Diagnostics[0].Message,
			"must remain valid Go comments",
		) {
		t.Fatalf("version 3 diagnostics = %+v", third.Diagnostics)
	}
	rawLine, _ := sourcePosition(raw, "@Application")
	client.send(map[string]any{
		"jsonrpc": "2.0",
		"id":      5,
		"method":  "textDocument/codeAction",
		"params": map[string]any{
			"textDocument": map[string]any{"uri": mainURI},
			"range": map[string]any{
				"start": map[string]any{"line": rawLine, "character": 0},
				"end":   map[string]any{"line": rawLine, "character": 12},
			},
			"context": map[string]any{
				"diagnostics": third.Diagnostics,
			},
		},
	})
	actionResponse := client.waitForID("5")
	var actions []protocolCodeAction
	if err := json.Unmarshal(actionResponse.Result, &actions); err != nil {
		t.Fatalf("Unmarshal(code actions) error = %v", err)
	}
	if len(actions) != 1 ||
		len(actions[0].Edit.DocumentChanges) != 1 ||
		actions[0].Edit.DocumentChanges[0].TextDocument.Version != 3 ||
		actions[0].Edit.DocumentChanges[0].Edits[0].NewText != "// " {
		t.Fatalf("code actions = %+v", actions)
	}

	legacyImport := strings.Replace(
		validWithConfigurationKey,
		"// @Application",
		`// @spice.import { Application } from "github.com/spice-framework/spice/annotation/core"

// @Application`,
		1,
	)
	client.change(mainURI, 4, legacyImport)
	legacyDiagnostics := client.waitForDiagnostics(mainURI, 4)
	if len(legacyDiagnostics.Diagnostics) != 1 ||
		legacyDiagnostics.Diagnostics[0].Code !=
			"spice.resolution.annotation-import-legacy" {
		t.Fatalf(
			"legacy import diagnostics = %+v",
			legacyDiagnostics.Diagnostics,
		)
	}
	client.send(map[string]any{
		"jsonrpc": "2.0",
		"id":      42,
		"method":  "textDocument/codeAction",
		"params": map[string]any{
			"textDocument": map[string]any{"uri": mainURI},
			"range":        legacyDiagnostics.Diagnostics[0].Range,
			"context": map[string]any{
				"diagnostics": legacyDiagnostics.Diagnostics,
			},
		},
	})
	legacyActionResponse := client.waitForID("42")
	var legacyActions []protocolCodeAction
	if err := json.Unmarshal(
		legacyActionResponse.Result,
		&legacyActions,
	); err != nil {
		t.Fatalf("Unmarshal(legacy import actions) error = %v", err)
	}
	if len(legacyActions) != 1 ||
		legacyActions[0].Title !=
			"Replace @spice.import with @import" ||
		len(legacyActions[0].Edit.DocumentChanges) != 1 ||
		legacyActions[0].Edit.DocumentChanges[0].
			TextDocument.Version != 4 ||
		legacyActions[0].Edit.DocumentChanges[0].
			Edits[0].NewText != "@import" {
		t.Fatalf("legacy import actions = %+v", legacyActions)
	}

	stale := strings.Replace(
		validWithConfigurationKey,
		"// @Application",
		"// @Application\n// @Unknown",
		1,
	)
	client.change(mainURI, 5, stale)
	client.change(mainURI, 6, validWithConfigurationKey)
	sixth := client.waitForDiagnostics(mainURI, 6)
	if len(sixth.Diagnostics) != 0 {
		t.Fatalf("version 6 diagnostics = %+v", sixth.Diagnostics)
	}
	if slices.Contains(client.diagnosticVersions, 5) {
		t.Fatalf(
			"server published stale version 5: %v",
			client.diagnosticVersions,
		)
	}

	client.notify("workspace/didChangeConfiguration", map[string]any{
		"settings": map[string]any{
			"spice": map[string]any{
				"patterns": []string{"./..."},
			},
		},
	})
	client.notify("textDocument/didSave", map[string]any{
		"textDocument": map[string]any{"uri": mainURI},
		"text":         stale,
	})
	savedInvalid := client.waitForDiagnostics(mainURI, 6)
	if len(savedInvalid.Diagnostics) == 0 {
		t.Fatal("saved invalid diagnostics are empty")
	}
	client.notify("textDocument/didSave", map[string]any{
		"textDocument": map[string]any{"uri": mainURI},
		"text":         validWithConfigurationKey,
	})
	savedValid := client.waitForDiagnostics(mainURI, 6)
	if len(savedValid.Diagnostics) != 0 {
		t.Fatalf("saved valid diagnostics = %+v", savedValid.Diagnostics)
	}
	client.notify("textDocument/didClose", map[string]any{
		"textDocument": map[string]any{"uri": mainURI},
	})
	closed := client.waitForClosedDiagnostics(mainURI)
	if closed.Diagnostics == nil {
		t.Fatal("closed diagnostics must be an empty JSON array, not null")
	}
	if len(closed.Diagnostics) != 0 || closed.Version != nil {
		t.Fatalf("closed diagnostics = %+v", closed)
	}
	client.notify("workspace/didChangeWorkspaceFolders", map[string]any{
		"event": map[string]any{
			"removed": []map[string]any{{"uri": rootURI, "name": "fixture"}},
			"added":   []map[string]any{{"uri": rootURI, "name": "fixture"}},
		},
	})
	client.send(map[string]any{
		"jsonrpc": "2.0",
		"id":      41,
		"method":  "textDocument/completion",
		"params": map[string]any{
			"textDocument": map[string]any{"uri": mainURI},
			"position":     map[string]any{"line": 0, "character": 0},
		},
	})
	_ = client.waitForID("41")
	server.mu.Lock()
	if len(server.workspaces) != 1 {
		server.mu.Unlock()
		t.Fatalf("workspace count = %d, want 1", len(server.workspaces))
	}
	for _, workspace := range server.workspaces {
		if !slices.Equal(workspace.patterns, []string{"./..."}) {
			server.mu.Unlock()
			t.Fatalf("workspace patterns = %v", workspace.patterns)
		}
	}
	server.mu.Unlock()

	client.send(map[string]any{
		"jsonrpc": "2.0",
		"id":      6,
		"method":  "shutdown",
	})
	if response := client.waitForID("6"); response.Error != nil {
		t.Fatalf("shutdown response = %+v", response)
	}
	client.notify("exit", nil)
	client.closeInput()
	if err := client.wait(); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestServerNavigatesImportedDescriptorAndImplementationUnderHostileTargetEnvironment(
	t *testing.T,
) {
	t.Parallel()
	root, mainPath, source := writeImportedLSPModule(t)
	server, err := New(Config{
		NewService: func(string) (*compilerservice.Service, error) {
			return compilerservice.New(compilerservice.Config{
				LoadOptions: load.Options{
					Env: hostileAnnotationToolEnvironment(),
				},
			})
		},
		AnalysisDelay: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	client := startTestClient(t, server)
	finished := false
	t.Cleanup(func() {
		if finished {
			return
		}
		if closeErr := client.input.Close(); closeErr != nil {
			t.Errorf("close test client input: %v", closeErr)
		}
		select {
		case <-client.done:
		case <-time.After(10 * time.Second):
		}
	})
	rootURI, err := fileURI(root)
	if err != nil {
		t.Fatal(err)
	}
	mainURI, err := fileURI(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	client.send(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"workspaceFolders": []map[string]any{{
				"uri":  rootURI,
				"name": "imported",
			}},
		},
	})
	if response := client.waitForID("1"); response.Error != nil {
		t.Fatalf("initialize response = %+v", response)
	}
	client.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri":        mainURI,
			"languageId": "go",
			"version":    1,
			"text":       source,
		},
	})
	if diagnostics := client.waitForDiagnostics(
		mainURI,
		1,
	); len(diagnostics.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %+v", diagnostics.Diagnostics)
	}
	usageOffset := strings.Index(source, "// @App") + len("// @")
	position := protocolPositionAtOffset([]byte(source), usageOffset)
	for id, method := range map[int]string{
		2: "textDocument/definition",
		3: "textDocument/implementation",
	} {
		client.send(map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"method":  method,
			"params": map[string]any{
				"textDocument": map[string]any{"uri": mainURI},
				"position":     position,
			},
		})
		response := client.waitForID(strconv.Itoa(id))
		var links []protocolLocationLink
		if unmarshalErr := json.Unmarshal(response.Result, &links); unmarshalErr != nil {
			t.Fatalf("Unmarshal(%s) error = %v", method, unmarshalErr)
		}
		expected := "/annotation/core/application.go"
		if len(links) != 1 ||
			!strings.HasSuffix(links[0].TargetURI, expected) ||
			links[0].OriginSelectionRange.Start.Line != position.Line ||
			links[0].OriginSelectionRange.Start.Character >
				position.Character ||
			links[0].OriginSelectionRange.End.Character <
				position.Character {
			t.Fatalf("%s links = %+v", method, links)
		}
	}
	client.send(map[string]any{
		"jsonrpc": "2.0",
		"id":      4,
		"method":  "textDocument/hover",
		"params": map[string]any{
			"textDocument": map[string]any{"uri": mainURI},
			"position":     position,
		},
	})
	hover := client.waitForID("4")
	for _, expected := range []string{
		"Application marks",
		"go tool github.com/spice-framework/toolchain/cmd/spice-annotation-core",
		"annotation/core.ApplicationHandler",
		"spice.annotation/v1alpha2",
		"local",
	} {
		if !strings.Contains(string(hover.Result), expected) {
			t.Fatalf("hover missing %q: %s", expected, hover.Result)
		}
	}
	client.send(map[string]any{
		"jsonrpc": "2.0",
		"id":      5,
		"method":  "shutdown",
		"params":  nil,
	})
	if response := client.waitForID("5"); response.Error != nil {
		t.Fatalf("shutdown response = %+v", response)
	}
	client.notify("exit", nil)
	client.closeInput()
	if err := client.wait(); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	finished = true
}

func hostileAnnotationToolEnvironment() []string {
	result := os.Environ()
	for _, setting := range []struct {
		name  string
		value string
	}{
		{name: "CGO_ENABLED", value: "1"},
		{name: "GOARCH", value: "amd64"},
		{name: "GOFLAGS", value: "-tags=ambient"},
		{name: "GOOS", value: "plan9"},
	} {
		filtered := make([]string, 0, len(result)+1)
		for _, entry := range result {
			name, _, found := strings.Cut(entry, "=")
			if found && strings.EqualFold(name, setting.name) {
				continue
			}
			filtered = append(filtered, entry)
		}
		result = filtered
		result = append(result, setting.name+"="+setting.value)
	}
	return result
}

func writeAnnotationReference(t *testing.T, root string) {
	t.Helper()
	path := filepath.Join(root, "docs", "annotations.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("MkdirAll(annotation reference) error = %v", err)
	}
	content := "# Definitions\n\n" +
		"| Annotation | Target |\n" +
		"|---|---|\n" +
		"| `@Application` | Function |\n" +
		"| `@management.Enable` | Function |\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile(annotation reference) error = %v", err)
	}
}

func TestServerRejectsExitWithoutShutdownAndInvalidConfiguration(t *testing.T) {
	t.Parallel()
	if _, err := New(Config{}); err == nil {
		t.Fatal("New(empty) error = nil, want failure")
	}
	if _, err := New(Config{
		NewService:       testServiceFactory,
		AnalysisDelay:    -time.Second,
		MaxDocumentBytes: 1,
	}); err == nil {
		t.Fatal("New(negative delay) error = nil, want failure")
	}
	server, err := New(Config{NewService: testServiceFactory})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	var input, output strings.Builder
	input.Write(frameJSON(t, map[string]any{
		"jsonrpc": "2.0",
		"method":  "exit",
	}))
	if err := server.Run(
		context.Background(),
		strings.NewReader(input.String()),
		&output,
	); !errors.Is(err, ErrExitWithoutShutdown) {
		t.Fatalf("Run(exit) error = %v", err)
	}
}

func TestServerCancelsActiveAnalysisForNewerDocumentVersion(t *testing.T) {
	t.Parallel()
	root, mainPath, original := writeLSPModule(t)
	firstStarted := make(chan struct{})
	firstCanceled := make(chan struct{})
	var calls atomic.Int64
	server, err := New(Config{
		NewService: func(string) (*compilerservice.Service, error) {
			return compilerservice.New(compilerservice.Config{
				Loader: func(
					ctx context.Context,
					options load.Options,
					patterns ...string,
				) (*load.Program, error) {
					if calls.Add(1) == 1 {
						close(firstStarted)
						<-ctx.Done()
						close(firstCanceled)
						return nil, ctx.Err()
					}
					return load.Load(ctx, options, patterns...)
				},
			})
		},
		AnalysisDelay: time.Millisecond,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	client := startTestClient(t, server)
	rootURI, err := fileURI(root)
	if err != nil {
		t.Fatalf("fileURI(root) error = %v", err)
	}
	mainURI, err := fileURI(mainPath)
	if err != nil {
		t.Fatalf("fileURI(main.go) error = %v", err)
	}
	client.send(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params":  map[string]any{"rootUri": rootURI},
	})
	_ = client.waitForID("1")
	client.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{
			"uri":        mainURI,
			"languageId": "go",
			"version":    1,
			"text":       original,
		},
	})
	select {
	case <-firstStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for first analysis")
	}
	invalid := strings.Replace(
		original,
		"// @Application",
		"// @Application\n// @Unknown",
		1,
	)
	client.change(mainURI, 2, invalid)
	second := client.waitForDiagnostics(mainURI, 2)
	if len(second.Diagnostics) == 0 {
		t.Fatal("newer analysis diagnostics are empty")
	}
	select {
	case <-firstCanceled:
	case <-time.After(10 * time.Second):
		t.Fatal("older analysis was not canceled")
	}
	client.send(map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "shutdown",
	})
	_ = client.waitForID("2")
	client.notify("exit", nil)
	client.closeInput()
	if err := client.wait(); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestServerRunHonorsCancellationWhileReading(t *testing.T) {
	t.Parallel()
	server, err := New(Config{NewService: testServiceFactory})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	input, writer := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- server.Run(ctx, input, io.Discard)
	}()
	cancel()
	select {
	case runErr := <-done:
		if !errors.Is(runErr, context.Canceled) {
			t.Fatalf("Run() error = %v, want context.Canceled", runErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run() did not honor cancellation")
	}
	if err := writer.Close(); err != nil &&
		!errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("Close(writer) error = %v", err)
	}
}

func TestServerReportsJSONRPCParseErrorAndContinues(t *testing.T) {
	t.Parallel()
	server, err := New(Config{NewService: testServiceFactory})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	var input bytes.Buffer
	input.WriteString("Content-Length: 1\r\n\r\n{")
	input.Write(frameJSON(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params":  map[string]any{},
	}))
	input.Write(frameJSON(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "shutdown",
	}))
	input.Write(frameJSON(t, map[string]any{
		"jsonrpc": "2.0",
		"method":  "exit",
	}))
	var output bytes.Buffer
	if err := server.Run(
		context.Background(),
		&input,
		&output,
	); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !bytes.Contains(output.Bytes(), []byte(`"code":-32700`)) ||
		!bytes.Contains(output.Bytes(), []byte(`"id":1`)) ||
		bytes.Count(output.Bytes(), []byte("Content-Length:")) != 3 {
		t.Fatalf("protocol output = %q", output.String())
	}
}

func TestAnalysisSettingsAndModuleRootBoundaries(t *testing.T) {
	t.Parallel()
	settings, err := normalizeAnalysisSettings(&analysisSettings{
		Target:   "Commerce",
		Patterns: []string{"./cmd/...", "./modules/..."},
		Profile:  "java-structured",
	})
	if err != nil ||
		settings.Target != "Commerce" ||
		settings.Profile != "java-structured" ||
		!slices.Equal(
			settings.Patterns,
			[]string{"./cmd/...", "./modules/..."},
		) {
		t.Fatalf("normalizeAnalysisSettings() = %+v, %v", settings, err)
	}
	styleSettings, err := normalizeAnalysisSettings(&analysisSettings{
		Style: ".spice/style.json",
	})
	if err != nil || styleSettings.Style != ".spice/style.json" {
		t.Fatalf("normalizeAnalysisSettings(style) = %+v, %v", styleSettings, err)
	}
	for _, invalid := range []analysisSettings{
		{Target: " Commerce"},
		{Patterns: []string{""}},
		{Patterns: []string{" ./..."}},
		{Profile: "java"},
		{Style: " .spice/style.json"},
		{Profile: "java-structured", Style: ".spice/style.json"},
	} {
		if _, normalizeErr := normalizeAnalysisSettings(&invalid); normalizeErr == nil {
			t.Errorf(
				"normalizeAnalysisSettings(%+v) error = nil",
				invalid,
			)
		}
	}
	root := t.TempDir()
	child := filepath.Join(root, "a", "b", "main.go")
	if mkdirErr := os.MkdirAll(filepath.Dir(child), 0o750); mkdirErr != nil {
		t.Fatalf("MkdirAll() error = %v", mkdirErr)
	}
	if writeErr := os.WriteFile(
		filepath.Join(root, "go.mod"),
		[]byte("module example.com/root\n\ngo 1.26.0\n"),
		0o600,
	); writeErr != nil {
		t.Fatalf("WriteFile(go.mod) error = %v", writeErr)
	}
	discovered, err := discoverModuleRoot(child)
	if err != nil || discovered != root {
		t.Fatalf("discoverModuleRoot() = %q, %v", discovered, err)
	}
	if _, err := discoverModuleRoot(
		filepath.Join(t.TempDir(), "main.go"),
	); err == nil {
		t.Fatal("discoverModuleRoot(no module) error = nil")
	}
}

func testServiceFactory(string) (*compilerservice.Service, error) {
	return compilerservice.New(compilerservice.Config{})
}

type testClient struct {
	t                  *testing.T
	input              *io.PipeWriter
	output             *bufio.Reader
	done               chan error
	diagnosticVersions []int
}

type serverEnvelope struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type publishedDiagnostics struct {
	URI         string               `json:"uri"`
	Version     *int                 `json:"version,omitempty"`
	Diagnostics []protocolDiagnostic `json:"diagnostics"`
}

func startTestClient(t *testing.T, server *Server) *testClient {
	t.Helper()
	serverInput, clientInput := io.Pipe()
	clientOutput, serverOutput := io.Pipe()
	done := make(chan error, 1)
	go func() {
		runErr := server.Run(
			context.Background(),
			serverInput,
			serverOutput,
		)
		closeErr := serverOutput.Close()
		done <- errors.Join(runErr, closeErr)
	}()
	return &testClient{
		t:      t,
		input:  clientInput,
		output: bufio.NewReader(clientOutput),
		done:   done,
	}
}

func (client *testClient) send(message map[string]any) {
	client.t.Helper()
	if _, err := client.input.Write(frameJSON(client.t, message)); err != nil {
		client.t.Fatalf("send() error = %v", err)
	}
}

func (client *testClient) notify(method string, params any) {
	client.t.Helper()
	client.send(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
}

func (client *testClient) change(uri string, version int, content string) {
	client.t.Helper()
	client.notify("textDocument/didChange", map[string]any{
		"textDocument": map[string]any{
			"uri":     uri,
			"version": version,
		},
		"contentChanges": []map[string]any{{"text": content}},
	})
}

func (client *testClient) waitForID(id string) serverEnvelope {
	client.t.Helper()
	for {
		message := client.read()
		if string(message.ID) == id {
			return message
		}
		client.recordDiagnostics(message)
	}
}

func (client *testClient) waitForDiagnostics(
	uri string,
	version int,
) publishedDiagnostics {
	client.t.Helper()
	for {
		message := client.read()
		diagnostics, found := client.recordDiagnostics(message)
		if found &&
			diagnostics.URI == uri &&
			diagnostics.Version != nil &&
			*diagnostics.Version == version {
			return diagnostics
		}
	}
}

func (client *testClient) waitForClosedDiagnostics(
	uri string,
) publishedDiagnostics {
	client.t.Helper()
	for {
		message := client.read()
		diagnostics, found := client.recordDiagnostics(message)
		if found && diagnostics.URI == uri && diagnostics.Version == nil {
			return diagnostics
		}
	}
}

func (client *testClient) recordDiagnostics(
	message serverEnvelope,
) (publishedDiagnostics, bool) {
	client.t.Helper()
	if message.Method != "textDocument/publishDiagnostics" {
		return publishedDiagnostics{}, false
	}
	var result publishedDiagnostics
	if err := json.Unmarshal(message.Params, &result); err != nil {
		client.t.Fatalf("Unmarshal(publishDiagnostics) error = %v", err)
	}
	if result.Version != nil {
		client.diagnosticVersions = append(
			client.diagnosticVersions,
			*result.Version,
		)
	}
	return result, true
}

func (client *testClient) read() serverEnvelope {
	client.t.Helper()
	type readResult struct {
		message serverEnvelope
		err     error
	}
	result := make(chan readResult, 1)
	go func() {
		message, err := readServerEnvelope(client.output)
		result <- readResult{message: message, err: err}
	}()
	select {
	case received := <-result:
		if received.err != nil {
			client.t.Fatalf("read() error = %v", received.err)
		}
		return received.message
	case <-time.After(testClientTimeout):
		client.t.Fatal("timed out waiting for LSP server message")
		return serverEnvelope{}
	}
}

func (client *testClient) closeInput() {
	client.t.Helper()
	if err := client.input.Close(); err != nil {
		client.t.Fatalf("Close(input) error = %v", err)
	}
}

func (client *testClient) wait() error {
	client.t.Helper()
	select {
	case err := <-client.done:
		return err
	case <-time.After(testClientTimeout):
		client.t.Fatal("timed out waiting for LSP server exit")
		return nil
	}
}

func readServerEnvelope(reader *bufio.Reader) (serverEnvelope, error) {
	length := 0
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return serverEnvelope{}, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if value, found := strings.CutPrefix(
			line,
			"Content-Length: ",
		); found {
			if _, err := fmt.Sscanf(value, "%d", &length); err != nil {
				return serverEnvelope{}, err
			}
		}
	}
	if length <= 0 {
		return serverEnvelope{}, fmt.Errorf(
			"invalid server Content-Length %d",
			length,
		)
	}
	content := make([]byte, length)
	if _, err := io.ReadFull(reader, content); err != nil {
		return serverEnvelope{}, err
	}
	var message serverEnvelope
	if err := json.Unmarshal(content, &message); err != nil {
		return serverEnvelope{}, err
	}
	return message, nil
}

func writeLSPModule(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("Abs(repository) error = %v", err)
	}
	coreDirectory := testsupport.CoreDirectory(t)
	original := `package main

import (
	"os"

	spiceapp "example.com/lspfixture/internal/spicegen/lspfixture"
)

// @Application
func main() {
	os.Exit(spiceapp.Main(os.Args[1:]))
}

// @import { Application } from "github.com/spice-framework/spice/annotation/core"
`
	files := map[string]string{
		"go.mod": "module example.com/lspfixture\n\ngo 1.26.0\n\n" +
			"tool github.com/spice-framework/toolchain/cmd/spice-annotation-core\n\n" +
			"require (\n" +
			"\tgithub.com/spice-framework/spice " + identity.CoreVersion + "\n" +
			"\tgithub.com/spice-framework/toolchain v0.0.0\n)\n\n" +
			"replace github.com/spice-framework/spice => " +
			filepath.ToSlash(coreDirectory) + "\n\n" +
			"replace github.com/spice-framework/toolchain => " +
			filepath.ToSlash(repository) + "\n",
		"main.go": original,
		"internal/spicegen/lspfixture/spice_command_gen.go": `//go:build !spice_generate

package spicegen

func Main([]string) int { return 0 }
`,
		"orders/doc.go": `// Package orders owns order configuration.
// @Module
package orders

// @import { Module } from "github.com/spice-framework/spice/annotation/modulith"
`,
		"orders/config.go": `package orders

// @ConfigurationProperties(prefix="orders")
type Settings struct {
	Limit int ` + "`spice:\"limit,default=100\"`" + `
}

// @import { ConfigurationProperties } from "github.com/spice-framework/spice/annotation/core"
`,
	}
	for relative, content := range files {
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", relative, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", relative, err)
		}
	}
	return root, filepath.Join(root, "main.go"), original
}

func writeImportedLSPModule(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("Abs(repository) error = %v", err)
	}
	coreDirectory := testsupport.CoreDirectory(t)
	source := `package main

import (
	"os"

	spiceapp "example.com/importedlsp/internal/spicegen/importedlsp"
)

// @import { Application as App } from "github.com/spice-framework/spice/annotation/core"

// @App
func main() {
	os.Exit(spiceapp.Main(os.Args[1:]))
}
`
	mod := "module example.com/importedlsp\n\ngo 1.26.0\n\n" +
		"tool github.com/spice-framework/toolchain/cmd/spice-annotation-core\n\n" +
		"require (\n" +
		"\tgithub.com/spice-framework/spice " + identity.CoreVersion + "\n" +
		"\tgithub.com/spice-framework/toolchain v0.0.0\n)\n\n" +
		"replace github.com/spice-framework/spice => " +
		filepath.ToSlash(coreDirectory) + "\n\n" +
		"replace github.com/spice-framework/toolchain => " +
		filepath.ToSlash(repository) + "\n"
	for relative, content := range map[string]string{
		"go.mod":  mod,
		"main.go": source,
		"internal/spicegen/importedlsp/spice_command_gen.go": `//go:build !spice_generate

package spicegen

func Main([]string) int { return 0 }
`,
	} {
		path := filepath.Join(root, relative)
		if mkdirErr := os.MkdirAll(filepath.Dir(path), 0o750); mkdirErr != nil {
			t.Fatalf("MkdirAll(%s) error = %v", relative, mkdirErr)
		}
		if writeErr := os.WriteFile(
			path,
			[]byte(content),
			0o600,
		); writeErr != nil {
			t.Fatalf("WriteFile(%s) error = %v", relative, writeErr)
		}
	}
	return root, filepath.Join(root, "main.go"), source
}

func sourcePosition(content, search string) (int, int) {
	offset := strings.Index(content, search)
	if offset < 0 {
		return -1, -1
	}
	position := protocolPositionAtOffset([]byte(content), offset)
	return position.Line, position.Character
}
