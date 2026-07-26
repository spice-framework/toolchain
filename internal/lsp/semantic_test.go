package lsp

import (
	"reflect"
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
		{line: 4, start: 3, length: 12, kind: semanticDecoratorToken},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("annotationSemanticTokens() = %#v, want %#v", got, want)
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
