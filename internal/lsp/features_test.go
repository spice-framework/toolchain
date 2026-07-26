package lsp

import (
	"slices"
	"strings"
	"testing"

	"github.com/StevenBuglione/spice/annotation"
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

func labels(items []completionItem) []string {
	result := make([]string, len(items))
	for index, item := range items {
		result[index] = item.Label
	}
	return result
}
