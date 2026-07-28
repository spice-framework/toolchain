package lsp

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/StevenBuglione/spice/annotation"
	"github.com/StevenBuglione/spice/compiler/diagnostic"
	compilerservice "github.com/StevenBuglione/spice/compiler/service"
)

func TestCompletionProducesValidGoCommentsAndTypedValues(t *testing.T) {
	t.Parallel()
	metadata := metadataView{
		definitions: []compilerservice.AnnotationDefinition{
			{
				Name:    "Application",
				Targets: []annotation.Target{annotation.TargetFunction},
			},
			{
				Name:    "management.Enable",
				Targets: []annotation.Target{annotation.TargetFunction},
				Arguments: []compilerservice.AnnotationArgument{{
					Name:             "expose",
					Kinds:            []annotation.Kind{annotation.KindList},
					ListElementKinds: []annotation.Kind{annotation.KindString},
					AllowedStrings:   []string{"health", "info"},
					Required:         true,
				}},
			},
			{
				Name:    "Module",
				Targets: []annotation.Target{annotation.TargetPackage},
				Arguments: []compilerservice.AnnotationArgument{{
					Name:  "allowedDependencies",
					Kinds: []annotation.Kind{annotation.KindList},
				}},
			},
		},
		modules: []compilerservice.Module{{
			ID: "example.com/shop/payments",
			NamedInterfaces: []compilerservice.NamedInterface{{
				Name: "spi",
			}},
		}},
		configurations: []compilerservice.Configuration{{
			Name: "Settings",
			Fields: []compilerservice.ConfigurationField{{
				Key:         "orders.limit",
				TypeID:      "int",
				Environment: "ORDERS_LIMIT",
			}},
		}},
	}
	raw := []byte("@man")
	rawItems := completionItems(raw, len(raw), metadata)
	if len(rawItems) != 1 ||
		rawItems[0].Label != "@management.Enable" ||
		!strings.HasPrefix(
			rawItems[0].TextEdit.NewText,
			`// @management.Enable(expose=["`,
		) {
		t.Fatalf("raw annotation completions = %+v", rawItems)
	}
	bareItems := completionItems([]byte("@"), 1, metadata)
	if got := labels(bareItems); !slices.Equal(got, []string{
		"@Application",
		"@management.Enable",
		"@Module",
	}) {
		t.Fatalf("bare annotation completions = %v", got)
	}
	for _, item := range bareItems {
		if !strings.HasPrefix(item.TextEdit.NewText, "// @") {
			t.Fatalf("bare annotation edit = %+v", item.TextEdit)
		}
	}

	management := []byte(`// @management.Enable(expose=["he"])`)
	managementOffset := strings.Index(string(management), "he") + 2
	valueItems := completionItems(
		management,
		managementOffset,
		metadata,
	)
	if got := labels(valueItems); !slices.Equal(
		got,
		[]string{"health", "info"},
	) {
		t.Fatalf("management value completions = %+v", valueItems)
	}
	if len(valueItems) == 0 {
		t.Fatal("management value completions are empty")
		return
	}
	if valueItems[0].TextEdit.NewText != "health" {
		t.Fatalf("management edit = %+v", valueItems[0].TextEdit)
	}

	module := []byte(`// @Module(allowedDependencies=[""])`)
	moduleOffset := strings.Index(string(module), `""`) + 1
	moduleItems := completionItems(module, moduleOffset, metadata)
	if got := labels(moduleItems); !slices.Equal(got, []string{
		"example.com/shop/payments",
		"example.com/shop/payments::spi",
	}) {
		t.Fatalf("module completions = %v", got)
	}

	configuration := []byte(`var key = "orders."`)
	configurationOffset := strings.Index(
		string(configuration),
		"orders.",
	) + len("orders.")
	configurationItems := completionItems(
		configuration,
		configurationOffset,
		metadata,
	)
	if got := labels(configurationItems); !slices.Equal(
		got,
		[]string{"orders.limit"},
	) {
		t.Fatalf("configuration completions = %v", got)
	}
}

func TestProtocolPositionsUseUTF16AndRejectSplitSurrogates(t *testing.T) {
	t.Parallel()
	content := []byte("a😀b\n")
	offsetAfterEmoji := len([]byte("a😀"))
	position := protocolPositionAtOffset(content, offsetAfterEmoji)
	if position != (protocolPosition{Line: 0, Character: 3}) {
		t.Fatalf("protocolPositionAtOffset() = %+v", position)
	}
	if offset, valid := byteOffset(
		content,
		protocolPosition{Line: 0, Character: 3},
	); !valid || offset != offsetAfterEmoji {
		t.Fatalf("byteOffset(character 3) = %d, %t", offset, valid)
	}
	if _, valid := byteOffset(
		content,
		protocolPosition{Line: 0, Character: 2},
	); valid {
		t.Fatal("byteOffset(split surrogate) valid = true")
	}
}

func TestAnnotationSnippetsCoverTypedRequiredArguments(t *testing.T) {
	t.Parallel()
	definition := compilerservice.AnnotationDefinition{
		Name: "fixture.Enable",
		Arguments: []compilerservice.AnnotationArgument{
			{
				Name:       "enabled",
				Kinds:      []annotation.Kind{annotation.KindBoolean},
				Required:   true,
				Positional: true,
			},
			{
				Name:     "count",
				Kinds:    []annotation.Kind{annotation.KindInteger},
				Required: true,
			},
			{
				Name:     "name",
				Kinds:    []annotation.Kind{annotation.KindString},
				Required: true,
			},
			{
				Name:     "values",
				Kinds:    []annotation.Kind{annotation.KindList},
				Required: true,
			},
		},
	}
	got := annotationSnippet(definition)
	for _, expected := range []string{
		"fixture.Enable(",
		"${1:true}",
		"count=${2:0}",
		`name="${3:value}"`,
		`values=["${4:value}"]`,
	} {
		if !strings.Contains(got, expected) {
			t.Fatalf(
				"annotationSnippet() = %q, missing %q",
				got,
				expected,
			)
		}
	}
}

func TestCompletionAndPositionBoundaries(t *testing.T) {
	t.Parallel()
	if items := completionItems(
		[]byte("package main"),
		len("package main"),
		metadataView{},
	); len(items) != 0 {
		t.Fatalf("completionItems(non-context) = %+v", items)
	}
	if items := completionItems(
		[]byte("var value = @App"),
		len("var value = @App"),
		metadataView{
			definitions: []compilerservice.AnnotationDefinition{{
				Name: "Application",
			}},
		},
	); len(items) != 0 {
		t.Fatalf("completionItems(invalid inline annotation) = %+v", items)
	}
	if _, valid := byteOffset(
		[]byte("one\n"),
		protocolPosition{Line: 2, Character: 0},
	); valid {
		t.Fatal("byteOffset(out of bounds) valid = true")
	}
	if got := protocolSeverity("unexpected"); got != 1 {
		t.Fatalf("protocolSeverity(unexpected) = %d", got)
	}
}

func TestCodeActionAnchorKeepsSeparatedAssertionEditRelevant(
	t *testing.T,
) {
	t.Parallel()
	content := []byte(
		"package main\n\n" +
			"// @Implements(Processor)\n" +
			"type Stripe struct{}\n",
	)
	path := filepath.Join(t.TempDir(), "main.go")
	version := 7
	anchor := diagnostic.SourceLocation(
		"",
		path,
		path,
		3,
		4,
		len("package main\n\n// "),
	)
	uri := anchor.URI
	insertion := diagnostic.SourceLocation("", path, path, 3, 1, 14)
	insertion.Range.End = insertion.Range.Start
	fix := diagnostic.SuggestedFix{
		Title:     "Add compile-time assertion for Processor",
		AppliesTo: &anchor,
		Edits: []diagnostic.TextEdit{{
			Location:        insertion,
			DocumentVersion: &version,
			NewText:         "var _ Processor = (*Stripe)(nil)\n\n",
		}},
	}
	source := document{
		uri:     uri,
		path:    path,
		version: version,
		content: content,
	}
	server := &Server{
		documents: map[string]*document{uri: &source},
	}
	request := protocolRangeFromCompiler(anchor.Range, content)
	action, relevant, safe := server.protocolCodeAction(
		source,
		request,
		fix,
	)
	if !safe || !relevant ||
		action.Edit == nil ||
		len(action.Edit.DocumentChanges) != 1 ||
		len(action.Edit.DocumentChanges[0].Edits) != 1 ||
		action.Edit.DocumentChanges[0].Edits[0].NewText !=
			"var _ Processor = (*Stripe)(nil)\n\n" {
		t.Fatalf(
			"protocolCodeAction() = %+v, relevant=%t safe=%t",
			action,
			relevant,
			safe,
		)
	}
}

func TestFileScopedDefinitionsPreserveAliasesAndRichDocumentation(
	t *testing.T,
) {
	t.Parallel()
	definitions := []compilerservice.AnnotationDefinition{
		{
			Name:              "core.Application",
			Summary:           "Defines the application.",
			Documentation:     "Application owns generated lifecycle behavior.",
			DescriptorPackage: "example.com/sdk/core",
			DescriptorSymbol:  "Application",
			Targets:           []annotation.Target{annotation.TargetFunction},
			Examples: []compilerservice.AnnotationExample{{
				Title: "Application",
				Code:  "// @App\nfunc main() {}",
			}},
			Compatibility: compilerservice.AnnotationCompatibility{
				Since:        "0.1.0",
				MinimumSpice: "0.1.0",
			},
			Implementation: compilerservice.AnnotationImplementation{
				Tool:     "example.com/sdk/cmd/annotations",
				Handler:  "example.com/sdk/core.ApplicationHandler",
				Protocol: "spice.annotation/v1alpha2",
				Package:  "example.com/sdk/core",
				Symbol:   "ApplicationHandler",
			},
			Provenance: compilerservice.AnnotationProvenance{
				Module:  "example.com/sdk",
				Version: "v1.2.3",
			},
		},
		{
			Name:              "web.Get",
			DescriptorPackage: "example.com/sdk/web",
			DescriptorSymbol:  "Get",
		},
	}
	content := []byte(
		"// @import { Application as App } from \"example.com/sdk/core\"\n" +
			"// @import * as http from \"example.com/sdk/web\"\n",
	)
	scoped := fileScopedDefinitions(content, definitions)
	if got := []string{scoped[0].Name, scoped[1].Name}; !slices.Equal(
		got,
		[]string{"App", "http.Get"},
	) {
		t.Fatalf("fileScopedDefinitions() names = %v", got)
	}
	documentation := annotationDocumentation(scoped[0])
	for _, expected := range []string{
		"Defines the application.",
		"generated lifecycle",
		"example.com/sdk/core.Application",
		"example.com/sdk@v1.2.3",
		"go tool example.com/sdk/cmd/annotations",
		"ApplicationHandler",
		"since Spice `0.1.0`",
	} {
		if !strings.Contains(documentation, expected) {
			t.Fatalf(
				"annotationDocumentation() missing %q:\n%s",
				expected,
				documentation,
			)
		}
	}
}

func TestAnnotationCompletionAddsExplicitImportsWithoutMagicResolution(
	t *testing.T,
) {
	t.Parallel()
	definition := compilerservice.AnnotationDefinition{
		Name:              "core.Application",
		DescriptorPackage: "example.com/sdk/core",
		DescriptorSymbol:  "Application",
		Targets:           []annotation.Target{annotation.TargetFunction},
	}
	content := []byte("package main\n\n@App")
	items := completionItems(content, len(content), metadataView{
		definitions: []compilerservice.AnnotationDefinition{definition},
	})
	if len(items) != 1 ||
		items[0].Label != "@Application" ||
		items[0].TextEdit.NewText != "// @Application" ||
		len(items[0].AdditionalEdits) != 1 ||
		!strings.Contains(
			items[0].AdditionalEdits[0].NewText,
			`// @import { Application } from "example.com/sdk/core"`,
		) {
		t.Fatalf("annotation completion = %+v", items)
	}
	edited := items[0].AdditionalEdits[0]
	if edited.Range.Start != (protocolPosition{Line: 1, Character: 0}) ||
		edited.Range.Start != edited.Range.End {
		t.Fatalf("annotation import edit = %+v", edited)
	}

	imported := []byte(
		"package main\n\n" +
			"// @import { Application as App } from \"example.com/sdk/core\"\n\n" +
			"// @A",
	)
	items = completionItems(imported, len(imported), metadataView{
		definitions: []compilerservice.AnnotationDefinition{definition},
	})
	if len(items) != 1 ||
		items[0].Label != "@App" ||
		len(items[0].AdditionalEdits) != 0 {
		t.Fatalf("imported annotation completion = %+v", items)
	}
}

func TestAnnotationImportCompletionShowsDescriptorProvenance(t *testing.T) {
	t.Parallel()
	definition := compilerservice.AnnotationDefinition{
		Name:              "web.Controller",
		DescriptorPackage: "example.com/sdk/annotation/web",
		DescriptorSymbol:  "Controller",
		Implementation: compilerservice.AnnotationImplementation{
			Tool: "example.com/sdk/cmd/annotations",
		},
		Provenance: compilerservice.AnnotationProvenance{
			Module:  "example.com/sdk",
			Version: "v1.4.0",
		},
	}
	symbolSource := []byte("// @import { Cont")
	symbols := completionItems(
		symbolSource,
		len(symbolSource),
		metadataView{
			definitions: []compilerservice.AnnotationDefinition{definition},
		},
	)
	if len(symbols) != 1 ||
		symbols[0].Label != "Controller" ||
		symbols[0].Detail != definition.DescriptorPackage {
		t.Fatalf("annotation import symbol completions = %+v", symbols)
	}

	pathSource := []byte(`// @import { Controller } from "example.com/s`)
	paths := completionItems(pathSource, len(pathSource), metadataView{
		definitions: []compilerservice.AnnotationDefinition{definition},
	})
	if len(paths) != 1 ||
		paths[0].Label != definition.DescriptorPackage ||
		!strings.Contains(paths[0].Detail, "example.com/sdk@v1.4.0") ||
		!strings.Contains(paths[0].Detail, "go tool example.com/sdk/cmd/annotations") {
		t.Fatalf("annotation import path completions = %+v", paths)
	}
}

func TestGoInterfaceCompletionComesFromTheCompilerCatalog(t *testing.T) {
	t.Parallel()
	content := []byte(
		"package main\n\n" +
			"// @import { ConformsTo as Implements } from \"example.com/sdk/core\"\n\n" +
			"// @Implements(payments.Pro)\n" +
			"type Stripe struct{}\n",
	)
	offset := strings.Index(string(content), "payments.Pro") +
		len("payments.Pro")
	items := completionItems(content, offset, metadataView{
		sourcePath: "C:/workspace/main.go",
		annotations: []compilerservice.Annotation{{
			Spelling: "Implements",
			SymbolID: "type:stripe",
			Location: diagnostic.Location{
				Path: "C:/workspace/main.go",
				Range: diagnostic.Range{
					Start: diagnostic.Position{Line: 5, Column: 4},
					End:   diagnostic.Position{Line: 5, Column: 14},
				},
			},
		}},
		definitions: []compilerservice.AnnotationDefinition{{
			Name:              "core.ConformsTo",
			DescriptorPackage: "example.com/sdk/core",
			DescriptorSymbol:  "ConformsTo",
			Arguments: []compilerservice.AnnotationArgument{{
				Name:        "interfaces",
				Kinds:       []annotation.Kind{annotation.KindIdentifier},
				ValueDomain: annotation.ValueDomainGoInterface,
				Positional:  true,
				Variadic:    true,
			}},
		}},
		goInterfaces: compilerservice.GoInterfaceCatalog{
			Packages: []compilerservice.GoInterfacePackage{
				{
					Name:  "main",
					Path:  "example.com/application",
					Files: []string{"C:/workspace/main.go"},
				},
				{
					Name: "payments",
					Path: "example.com/application/payments",
					Interfaces: []compilerservice.GoInterface{{
						Name:        "Processor",
						PackageName: "payments",
						PackagePath: "example.com/application/payments",
						TypeID: "example.com/application/payments." +
							"Processor",
						Exported: true,
						Methods: []compilerservice.GoInterfaceMethod{{
							Name:      "Process",
							Signature: "func() error",
						}},
					}},
				},
			},
		},
	})
	if len(items) != 1 ||
		items[0].Label != "payments.Processor" ||
		items[0].TextEdit.NewText != "payments.Processor" ||
		len(items[0].AdditionalEdits) != 1 ||
		items[0].AdditionalEdits[0].NewText !=
			"// @import * as payments from \"example.com/application/payments\"\n" ||
		items[0].Documentation == nil ||
		!strings.Contains(
			items[0].Documentation.Value,
			"Resolved by Spice's typed Go program",
		) {
		t.Fatalf("Go interface completion = %+v", items)
	}
	if items[0].TextEdit.Range != (protocolRange{
		Start: protocolPosition{Line: 4, Character: 15},
		End:   protocolPosition{Line: 4, Character: 27},
	}) {
		t.Fatalf("interface text edit = %+v", items[0].TextEdit)
	}
	if items[0].AdditionalEdits[0].Range != (protocolRange{
		Start: protocolPosition{Line: 3, Character: 0},
		End:   protocolPosition{Line: 3, Character: 0},
	}) {
		t.Fatalf(
			"Spice namespace import edit = %+v",
			items[0].AdditionalEdits[0],
		)
	}
}

func TestGoInterfaceCompletionUsesExistingAliasAndGenericSnippet(
	t *testing.T,
) {
	t.Parallel()
	content := []byte(
		"package main\n\n" +
			"// @import { Bind } from \"example.com/sdk/core\"\n" +
			"// @import * as pay from \"example.com/application/payments\"\n\n" +
			"// @Bind(pay.Rep)\n" +
			"type Store struct{}\n",
	)
	offset := strings.Index(string(content), "pay.Rep") + len("pay.Rep")
	items := completionItems(content, offset, metadataView{
		sourcePath: "C:/workspace/main.go",
		definitions: []compilerservice.AnnotationDefinition{{
			Name:              "Bind",
			DescriptorPackage: "example.com/sdk/core",
			DescriptorSymbol:  "Bind",
			Arguments: []compilerservice.AnnotationArgument{{
				Name:        "contracts",
				Kinds:       []annotation.Kind{annotation.KindIdentifier},
				ValueDomain: annotation.ValueDomainGoInterface,
				Positional:  true,
			}},
		}},
		goInterfaces: compilerservice.GoInterfaceCatalog{
			Packages: []compilerservice.GoInterfacePackage{{
				Name: "payments",
				Path: "example.com/application/payments",
				Interfaces: []compilerservice.GoInterface{{
					Name:           "Repository",
					PackageName:    "payments",
					PackagePath:    "example.com/application/payments",
					TypeParameters: []string{"T"},
					Exported:       true,
				}},
			}},
		},
	})
	if len(items) != 1 ||
		items[0].Label != "pay.Repository" ||
		items[0].TextEdit.NewText != "pay.Repository[${1:T}]" ||
		items[0].InsertTextFormat != 2 ||
		len(items[0].AdditionalEdits) != 0 {
		t.Fatalf("aliased generic interface completion = %+v", items)
	}
}

func TestGoInterfaceCompletionUsesRealCompilerAuthoringMetadata(
	t *testing.T,
) {
	t.Parallel()
	root := t.TempDir()
	repository, absolutePathErr := filepath.Abs(filepath.Join("..", ".."))
	if absolutePathErr != nil {
		t.Fatalf("Abs(repository) error = %v", absolutePathErr)
	}
	source := []byte(`package main

// @import { Implements, Service } from "github.com/StevenBuglione/spice/annotation/core"

// @Service
// @Implements(payments.Pro)
type Stripe struct{}
`)
	files := map[string]string{
		"go.mod": "module example.com/interfacecompletion\n\n" +
			"go 1.26.0\n\n" +
			"tool github.com/StevenBuglione/spice/cmd/" +
			"spice-annotation-core\n\n" +
			"require github.com/StevenBuglione/spice v0.0.0\n\n" +
			"replace github.com/StevenBuglione/spice => " +
			filepath.ToSlash(repository) + "\n",
		"main.go": string(source),
		"payments/payments.go": `package payments

type Processor interface {
	Process() error
}
`,
	}
	for relative, content := range files {
		filePath := filepath.Join(root, filepath.FromSlash(relative))
		if mkdirErr := os.MkdirAll(
			filepath.Dir(filePath),
			0o750,
		); mkdirErr != nil {
			t.Fatalf("MkdirAll(%s) error = %v", relative, mkdirErr)
		}
		if writeErr := os.WriteFile(
			filePath,
			[]byte(content),
			0o600,
		); writeErr != nil {
			t.Fatalf("WriteFile(%s) error = %v", relative, writeErr)
		}
	}
	compiler, err := compilerservice.New(compilerservice.Config{})
	if err != nil {
		t.Fatalf("service.New() error = %v", err)
	}
	t.Cleanup(func() {
		if closeErr := compiler.Close(context.Background()); closeErr != nil {
			t.Errorf("Service.Close() error = %v", closeErr)
		}
	})
	mainPath := filepath.Join(root, "main.go")
	result, err := compiler.Analyze(t.Context(), compilerservice.Request{
		WorkspaceRoot: root,
		Mode:          compilerservice.AnalysisValidate,
		Overlay: map[string]compilerservice.Document{
			mainPath: {Version: 1, Content: source},
		},
	})
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	offset := strings.Index(string(source), "payments.Pro") +
		len("payments.Pro")
	items := completionItems(source, offset, metadataView{
		sourcePath:   mainPath,
		definitions:  result.AnnotationDefinitions(),
		annotations:  result.Annotations(),
		providers:    result.ProviderGraph().Providers,
		goInterfaces: result.GoInterfaces(),
	})
	if len(items) != 1 ||
		items[0].Label != "payments.Processor" ||
		len(items[0].AdditionalEdits) != 1 ||
		!strings.Contains(
			items[0].AdditionalEdits[0].NewText,
			`// @import * as payments from "example.com/interfacecompletion/payments"`,
		) {
		t.Fatalf(
			"real compiler interface completion = %+v; annotations=%+v; providers=%+v",
			items,
			result.Annotations(),
			result.ProviderGraph().Providers,
		)
	}
}

func TestCompletionDiscoversThirdPartyDescriptorsFromOfflineModuleGraph(
	t *testing.T,
) {
	t.Parallel()
	root, err := filepath.Abs(filepath.Join(
		"..",
		"..",
		"testdata",
		"annotationapp",
	))
	if err != nil {
		t.Fatalf("resolve annotation application fixture: %v", err)
	}
	compiler, err := compilerservice.New(compilerservice.Config{})
	if err != nil {
		t.Fatalf("service.New() error = %v", err)
	}
	t.Cleanup(func() {
		if closeErr := compiler.Close(context.Background()); closeErr != nil {
			t.Errorf("Service.Close() error = %v", closeErr)
		}
	})
	server := &Server{
		workspaces: map[string]*workspace{
			pathKey(root): {
				root:    root,
				service: compiler,
			},
		},
	}
	definitions := server.catalogCompletionDefinitions(root, nil)
	content := []byte("package main\n\n@Fac")
	items := completionItems(content, len(content), metadataView{
		definitions: definitions,
	})
	if len(items) != 1 ||
		items[0].Label != "@Factory" ||
		len(items[0].AdditionalEdits) != 1 ||
		!strings.Contains(
			items[0].AdditionalEdits[0].NewText,
			`// @import { Factory } from "example.com/spice-annotation-fixture/annotation/wiring"`,
		) ||
		!strings.Contains(items[0].Detail, "v0.0.0") ||
		!strings.Contains(items[0].Detail, "go tool") ||
		!strings.Contains(items[0].Detail, "tool declared") ||
		items[0].Documentation == nil ||
		!strings.Contains(
			items[0].Documentation.Value,
			"local replacement",
		) &&
			!strings.Contains(
				items[0].Documentation.Value,
				"Replaced by",
			) {
		t.Fatalf("third-party catalog completion = %+v", items)
	}
}

func TestSignatureHelpUsesImportedAliasAndArgumentDocumentation(t *testing.T) {
	t.Parallel()
	source := []byte(
		"// @import { Configuration as Config } from \"example.com/sdk/core\"\n" +
			"// @Config(prefix=\"orders\")\n" +
			"type Settings struct{}\n",
	)
	offset := strings.Index(string(source), `prefix="orders"`) + len("prefix")
	result, found := signatureHelpAt(
		source,
		offset,
		[]compilerservice.AnnotationDefinition{{
			Name:              "core.Configuration",
			Summary:           "Declares typed configuration.",
			DescriptorPackage: "example.com/sdk/core",
			DescriptorSymbol:  "Configuration",
			Arguments: []compilerservice.AnnotationArgument{{
				Name:        "prefix",
				Kinds:       []annotation.Kind{annotation.KindString},
				Description: "Optional property-key prefix.",
				Default:     "application",
			}},
		}},
	)
	if !found ||
		len(result.Signatures) != 1 ||
		result.Signatures[0].Label != "@Config(prefix: string?)" ||
		!strings.Contains(
			result.Signatures[0].Parameters[0].Documentation.Value,
			"Optional property-key prefix.",
		) ||
		!strings.Contains(
			result.Signatures[0].Parameters[0].Documentation.Value,
			"Default: `application`",
		) {
		t.Fatalf("signatureHelpAt() = %+v, %t", result, found)
	}
}

func labels(items []completionItem) []string {
	result := make([]string, len(items))
	for index, item := range items {
		result[index] = item.Label
	}
	return result
}
