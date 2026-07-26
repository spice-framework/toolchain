package lsp

import (
	"reflect"
	"slices"
	"testing"
)

func TestAnnotationSemanticTokens(t *testing.T) {
	t.Parallel()
	content := []byte(
		"package main\n\n" +
			"\t// @management.Enable(expose=[\"health\"])\r\n" +
			"// ordinary comment\n" +
			"// @Application\n" +
			"func main() {}\n",
	)
	got := annotationSemanticTokens(content)
	want := []semanticToken{
		{line: 2, start: 4, length: 18, kind: semanticDecoratorToken},
		{line: 2, start: 22, length: 1, kind: semanticOperatorToken},
		{line: 2, start: 23, length: 6, kind: semanticParameterToken},
		{line: 2, start: 29, length: 1, kind: semanticOperatorToken},
		{line: 2, start: 30, length: 1, kind: semanticOperatorToken},
		{line: 2, start: 31, length: 8, kind: semanticStringToken},
		{line: 2, start: 39, length: 1, kind: semanticOperatorToken},
		{line: 2, start: 40, length: 1, kind: semanticOperatorToken},
		{line: 4, start: 3, length: 12, kind: semanticDecoratorToken},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("annotationSemanticTokens() = %#v, want %#v", got, want)
	}
}

func TestAnnotationSemanticTokensClassifyTypedArguments(t *testing.T) {
	t.Parallel()
	content := []byte(`// @fixture.Enable(enabled=true, count=-12, mode=fast)`)
	got := annotationSemanticTokens(content)
	kinds := make([]int, len(got))
	for index, token := range got {
		kinds[index] = token.kind
	}
	for _, required := range []int{
		semanticDecoratorToken,
		semanticParameterToken,
		semanticKeywordToken,
		semanticNumberToken,
		semanticOperatorToken,
	} {
		if !slices.Contains(kinds, required) {
			t.Fatalf("semantic token kinds = %v, missing %d", kinds, required)
		}
	}
}

func TestAnnotationSemanticTokensRejectsInvalidPresentation(t *testing.T) {
	t.Parallel()
	for _, content := range []string{
		"@Application",
		"const value = \"// @Application\"",
		"// @",
		"// #Application",
	} {
		if got := annotationSemanticTokens([]byte(content)); len(got) != 0 {
			t.Fatalf("annotationSemanticTokens(%q) = %#v, want none", content, got)
		}
	}
}

func TestEncodeSemanticTokensUsesRelativeUTF16Positions(t *testing.T) {
	t.Parallel()
	tokens := []semanticToken{
		{line: 2, start: 4, length: 18, kind: semanticDecoratorToken},
		{line: 2, start: 28, length: 5, kind: semanticDecoratorToken},
		{line: 4, start: 3, length: 12, kind: semanticDecoratorToken},
	}
	got := encodeSemanticTokens(tokens)
	want := []uint32{
		2, 4, 18, 0, 0,
		0, 24, 5, 0, 0,
		2, 3, 12, 0, 0,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("encodeSemanticTokens() = %#v, want %#v", got, want)
	}
}

func TestAnnotationSemanticTokenCountsUTF16(t *testing.T) {
	t.Parallel()
	token, found := annotationSemanticToken(
		[]byte("\t// \t@observability.Logging"),
		0,
	)
	if !found {
		t.Fatal("annotationSemanticToken() found = false")
	}
	if token.start != 5 || token.length != 22 {
		t.Fatalf("annotationSemanticToken() = %#v", token)
	}
}
