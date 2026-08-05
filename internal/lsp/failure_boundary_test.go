package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/spice-framework/spice/annotation/sdk"
	"github.com/spice-framework/toolchain/compiler/diagnostic"
	compilerservice "github.com/spice-framework/toolchain/compiler/service"
)

func TestSignatureHelpProtocolFailureBoundaries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		message rpcMessage
		server  *Server
		code    int
	}{
		{
			name:    "notification ignored",
			message: rpcMessage{Params: json.RawMessage(`{}`)},
			server:  &Server{},
		},
		{
			name:    "malformed parameters",
			message: rpcMessage{ID: json.RawMessage(`1`), Params: json.RawMessage(`[]`)},
			server:  &Server{},
			code:    invalidParamsCode,
		},
		{
			name: "unknown document",
			message: rpcMessage{
				ID:     json.RawMessage(`2`),
				Params: json.RawMessage(`{"textDocument":{"uri":"file:///missing.go"},"position":{"line":0,"character":0}}`),
			},
			server: &Server{documents: map[string]*document{}, workspaces: map[string]*workspace{}},
		},
		{
			name: "invalid UTF-16 position",
			message: rpcMessage{
				ID:     json.RawMessage(`3`),
				Params: json.RawMessage(`{"textDocument":{"uri":"file:///source.go"},"position":{"line":20,"character":0}}`),
			},
			server: &Server{
				documents:  map[string]*document{"file:///source.go": {uri: "file:///source.go", content: []byte("package sample\n")}},
				workspaces: map[string]*workspace{},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var output bytes.Buffer
			test.server.writer = newRPCWriter(&output)
			if err := test.server.signatureHelp(test.message); err != nil {
				t.Fatalf("signatureHelp() error = %v", err)
			}
			if !test.message.request() {
				if output.Len() != 0 {
					t.Fatalf("notification output = %q", output.String())
				}
				return
			}
			envelope, err := readServerEnvelope(bufio.NewReader(bytes.NewReader(output.Bytes())))
			if err != nil {
				t.Fatalf("read response: %v", err)
			}
			if test.code != 0 {
				if envelope.Error == nil || envelope.Error.Code != test.code {
					t.Fatalf("response error = %+v, want code %d", envelope.Error, test.code)
				}
				return
			}
			if string(envelope.Result) != "null" {
				t.Fatalf("response result = %s, want null", envelope.Result)
			}
		})
	}
}

func TestGoImportInspectionAndInterfaceResolutionFailures(t *testing.T) {
	t.Parallel()

	if state := inspectGoImports([]byte("package broken\nimport (")); len(state.byPath) != 0 {
		t.Fatalf("malformed imports = %v", state.byPath)
	}
	content := []byte(`package sample
import (
	alias "example.com/contracts"
	_ "example.com/sideeffect"
	. "example.com/dot"
	"example.com/plain"
)
`)
	imports := inspectGoImports(content)
	if imports.byPath["example.com/contracts"] != "alias" ||
		imports.byPath["example.com/plain"] != "" {
		t.Fatalf("imports = %+v", imports)
	}
	for _, path := range []string{"example.com/sideeffect", "example.com/dot"} {
		if _, found := imports.unusable[path]; !found {
			t.Fatalf("unusable imports missing %q: %+v", path, imports)
		}
	}

	occupied := mergedNamespaceOccupancy(imports, spiceNamespaceState{
		occupied: map[string]struct{}{"plain2": {}},
	})
	if got := availableGoQualifier("plain", occupied); got != "plain3" {
		t.Fatalf("availableGoQualifier() = %q, want plain3", got)
	}
	if qualifier, add, available := interfaceQualifier(
		imports,
		spiceNamespaceState{},
		compilerservice.GoInterfacePackage{Name: "sideeffect", Path: "example.com/sideeffect"},
		false,
	); qualifier != "" || add || available {
		t.Fatalf("unusable interface qualifier = %q, %t, %t", qualifier, add, available)
	}
	if qualifier, add, available := interfaceQualifier(
		imports,
		spiceNamespaceState{},
		compilerservice.GoInterfacePackage{Path: "example.com/unnamed"},
		false,
	); qualifier != "" || add || available {
		t.Fatalf("unnamed interface qualifier = %q, %t, %t", qualifier, add, available)
	}
}

func TestGoInterfaceReferenceUsesOrdinaryAndSpiceImports(t *testing.T) {
	t.Parallel()

	definition := compilerservice.AnnotationDefinition{
		Name:              "core.Implements",
		DescriptorPackage: "example.com/sdk/core",
		DescriptorSymbol:  "Implements",
		Arguments: []compilerservice.AnnotationArgument{{
			Name:        "interfaces",
			Positional:  true,
			ValueDomain: sdk.ValueDomainGoInterface,
		}},
	}
	contract := compilerservice.GoInterface{
		Name:        "Processor",
		PackageName: "contracts",
		PackagePath: "example.com/contracts",
		HasLocation: true,
		Location: diagnostic.Location{
			URI:   "file:///contracts/processor.go",
			Range: diagnostic.Range{Start: diagnostic.Position{Line: 4, Column: 6}},
		},
	}

	for _, source := range []string{
		"package sample\nimport alias \"example.com/contracts\"\n// @import { Implements } from \"example.com/sdk/core\"\n// @Implements(alias.Processor)\ntype Service struct{}\n",
		"package sample\n// @import { Implements } from \"example.com/sdk/core\"\n// @import * as api from \"example.com/contracts\"\n// @Implements(api.Processor)\ntype Service struct{}\n",
	} {
		content := []byte(source)
		offset := bytes.Index(content, []byte("Processor")) + 2
		reference, found := goInterfaceReferenceAt(document{
			path:    filepath.Join(t.TempDir(), "source.go"),
			content: content,
		}, metadataView{
			definitions: []compilerservice.AnnotationDefinition{definition},
			goInterfaces: compilerservice.GoInterfaceCatalog{Packages: []compilerservice.GoInterfacePackage{{
				Name:       "contracts",
				Path:       "example.com/contracts",
				Interfaces: []compilerservice.GoInterface{contract},
			}}},
		}, offset)
		if !found || reference.contract.Name != "Processor" || string(content[reference.start:reference.end]) == "" {
			t.Fatalf("goInterfaceReferenceAt() = %+v, %t", reference, found)
		}
	}
}

func TestLocationContentAndAnalysisErrorBoundaries(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	regular := filepath.Join(root, "regular.go")
	if err := os.WriteFile(regular, []byte("package regular\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	uri := "file:///overlay.go"
	server := &Server{
		config: serverConfig{maxDocumentBytes: 32},
		documents: map[string]*document{
			uri: {uri: uri, path: filepath.Join(root, "overlay.go"), content: []byte("overlay")},
		},
	}
	server.mu.Lock()
	if got := server.locationContentLocked(diagnostic.Location{URI: uri}); string(got) != "overlay" {
		server.mu.Unlock()
		t.Fatalf("URI overlay content = %q", got)
	}
	if got := server.locationContentLocked(diagnostic.Location{Path: filepath.Join(root, "overlay.go")}); string(got) != "overlay" {
		server.mu.Unlock()
		t.Fatalf("path overlay content = %q", got)
	}
	if got := server.locationContentLocked(diagnostic.Location{Path: regular}); string(got) != "package regular\n" {
		server.mu.Unlock()
		t.Fatalf("disk content = %q", got)
	}
	for _, path := range []string{filepath.Join(root, "missing.go"), root} {
		if got := server.locationContentLocked(diagnostic.Location{Path: path}); got != nil {
			server.mu.Unlock()
			t.Fatalf("invalid path %q content = %q", path, got)
		}
	}
	server.mu.Unlock()

	var output bytes.Buffer
	server.writer = newRPCWriter(&output)
	server.showError(nil)
	server.showError(errors.New("catalog unavailable"))
	message, err := readServerEnvelope(bufio.NewReader(bytes.NewReader(output.Bytes())))
	if err != nil || message.Method != "window/showMessage" || !strings.Contains(string(message.Params), "catalog unavailable") {
		t.Fatalf("showError response = %+v, %v", message, err)
	}

	cancelled, cancel := context.WithCancel(t.Context())
	server.cancel = cancel
	server.writer = newRPCWriter(errorWriterForLSP{})
	server.showError(errors.New("write failed"))
	if !errors.Is(cancelled.Err(), context.Canceled) {
		t.Fatalf("write failure did not cancel server: %v", cancelled.Err())
	}
}

func TestProtocolDiagnosticPreservesRelatedLocationsAndSeverity(t *testing.T) {
	t.Parallel()

	severities := []diagnostic.Severity{
		diagnostic.SeverityError,
		diagnostic.SeverityWarning,
		diagnostic.SeverityInformation,
		diagnostic.SeverityHint,
		diagnostic.Severity("unknown"),
	}
	want := []int{1, 2, 3, 4, 1}
	got := make([]int, 0, len(severities))
	for _, severity := range severities {
		got = append(got, protocolSeverity(severity))
	}
	if !slices.Equal(got, want) {
		t.Fatalf("protocol severities = %v, want %v", got, want)
	}

	item := diagnostic.Diagnostic{
		Severity: diagnostic.SeverityWarning,
		Code:     "spice.import.invalid",
		Message:  "invalid import",
		Related: []diagnostic.RelatedInformation{
			{Message: "source without URI"},
			{
				Message: "descriptor",
				Location: diagnostic.Location{
					URI:   "file:///descriptor.go",
					Range: diagnostic.Range{Start: diagnostic.Position{Line: 3, Column: 2}},
				},
			},
		},
	}
	converted := protocolDiagnosticFromCompiler(item, nil)
	if converted.Severity != 2 || len(converted.Related) != 1 || converted.Related[0].Message != "descriptor" {
		t.Fatalf("protocol diagnostic = %+v", converted)
	}
}

func TestNavigationProtocolFailureBoundaries(t *testing.T) {
	t.Parallel()

	uri := "file:///source.go"
	server := &Server{
		documents: map[string]*document{
			uri: {uri: uri, content: []byte("package sample\n// ordinary comment\n")},
		},
		workspaces: map[string]*workspace{},
	}
	requests := []struct {
		name   string
		params json.RawMessage
		call   func(rpcMessage) error
	}{
		{
			name:   "definition malformed",
			params: json.RawMessage(`[]`),
			call:   server.definition,
		},
		{
			name:   "definition unknown document",
			params: positionParams("file:///missing.go", 0, 0),
			call:   server.definition,
		},
		{
			name:   "definition invalid position",
			params: positionParams(uri, 99, 0),
			call:   server.definition,
		},
		{
			name:   "definition no symbol",
			params: positionParams(uri, 0, 1),
			call:   server.definition,
		},
		{
			name:   "implementation malformed",
			params: json.RawMessage(`[]`),
			call:   server.implementation,
		},
		{
			name:   "implementation no symbol",
			params: positionParams(uri, 0, 1),
			call:   server.implementation,
		},
		{
			name:   "document links malformed",
			params: json.RawMessage(`[]`),
			call:   server.documentLinks,
		},
		{
			name:   "document links unknown document",
			params: json.RawMessage(`{"textDocument":{"uri":"file:///missing.go"}}`),
			call:   server.documentLinks,
		},
	}
	for index, request := range requests {
		t.Run(request.name, func(t *testing.T) {
			var output bytes.Buffer
			server.writer = newRPCWriter(&output)
			if err := request.call(rpcMessage{ID: json.RawMessage(`1`), Params: request.params}); err != nil {
				t.Fatalf("navigation request %d: %v", index, err)
			}
			if output.Len() == 0 {
				t.Fatal("navigation request produced no response")
			}
		})
	}
	for _, call := range []func(rpcMessage) error{server.definition, server.implementation, server.documentLinks} {
		var output bytes.Buffer
		server.writer = newRPCWriter(&output)
		if err := call(rpcMessage{}); err != nil || output.Len() != 0 {
			t.Fatalf("notification = %v, %q", err, output.String())
		}
	}
}

func TestEnsureWorkspaceReportsDiscoveryAndConfigurationFailures(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source := filepath.Join(root, "nested", "source.go")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	server := &Server{workspaces: map[string]*workspace{
		pathKey(root): {root: root},
	}}
	if key, err := server.ensureWorkspace(source); err != nil || key != pathKey(root) {
		t.Fatalf("existing ensureWorkspace() = %q, %v", key, err)
	}

	missing := &Server{workspaces: map[string]*workspace{}}
	if _, err := missing.ensureWorkspace(source); err == nil || !strings.Contains(err.Error(), "go.mod not found") {
		t.Fatalf("missing module ensureWorkspace() error = %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/sample\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	want := errors.New("service unavailable")
	failed := &Server{
		config:     serverConfig{newService: func(string) (*compilerservice.Service, error) { return nil, want }},
		workspaces: map[string]*workspace{},
	}
	if _, err := failed.ensureWorkspace(source); !errors.Is(err, want) {
		t.Fatalf("failed ensureWorkspace() error = %v, want %v", err, want)
	}
}

func positionParams(uri string, line, character int) json.RawMessage {
	return json.RawMessage(`{"textDocument":{"uri":"` + uri + `"},"position":{"line":` +
		jsonNumber(line) + `,"character":` + jsonNumber(character) + `}}`)
}

func jsonNumber(value int) string {
	return strconv.Itoa(value)
}

type errorWriterForLSP struct{}

func (errorWriterForLSP) Write([]byte) (int, error) {
	return 0, errors.New("forced write failure")
}
