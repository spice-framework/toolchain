package parser

import (
	"go/token"
	"strings"
	"testing"

	"github.com/StevenBuglione/spice/annotation"
)

func TestParseImportComment(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  annotation.ImportDirective
	}{
		{
			name:  "named",
			input: `// @import { Application } from "example.com/spice/core"`,
			want: annotation.ImportDirective{
				Kind:    annotation.ImportNamed,
				Package: "example.com/spice/core",
				Bindings: []annotation.ImportBinding{{
					Imported: "Application",
					Local:    "Application",
				}},
			},
		},
		{
			name:  "named aliases",
			input: `// @import { Controller, Get as GET } from "example.com/spice/web"`,
			want: annotation.ImportDirective{
				Kind:    annotation.ImportNamed,
				Package: "example.com/spice/web",
				Bindings: []annotation.ImportBinding{
					{Imported: "Controller", Local: "Controller"},
					{Imported: "Get", Local: "GET"},
				},
			},
		},
		{
			name:  "namespace",
			input: `// @import * as web from "example.com/spice/web"`,
			want: annotation.ImportDirective{
				Kind:      annotation.ImportNamespace,
				Package:   "example.com/spice/web",
				Namespace: "web",
			},
		},
	}
	position := token.Position{Filename: "main.go", Line: 7, Column: 1}
	annotationPosition := position
	annotationPosition.Offset += len("// ")
	annotationPosition.Column += len("// ")
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok, err := ParseImportComment(test.input, position)
			if err != nil || !ok {
				t.Fatalf("ParseImportComment() = %#v, %t, %v", got, ok, err)
			}
			test.want.Position = annotationPosition
			test.want.Raw = test.input
			if !equalImport(got, test.want) {
				t.Fatalf("ParseImportComment() = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestParseImportCommentIgnoresOrdinaryAnnotations(t *testing.T) {
	for _, input := range []string{
		"// @Application",
		"// @imported",
		"// @spice.imported",
	} {
		got, ok, err := ParseImportComment(
			input,
			token.Position{Filename: "main.go", Line: 1, Column: 1},
		)
		if err != nil || ok || got.Kind != "" {
			t.Fatalf("ParseImportComment(%q) = %#v, %t, %v", input, got, ok, err)
		}
	}
}

func TestParseImportCommentRejectsMalformedImports(t *testing.T) {
	tests := []struct {
		input   string
		message string
	}{
		{`// @import`, "binding clause"},
		{`// @import {}`, "at least one symbol"},
		{`// @import { private } from "example.com/a"`, "exported"},
		{`// @import { A, A } from "example.com/a"`, "repeats symbol"},
		{`// @import { A, } from "example.com/a"`, "trailing commas"},
		{`// @import * web from "example.com/a"`, "* as alias"},
		{`// @import * as _ from "example.com/a"`, "usable alias"},
		{`// @import { A } "example.com/a"`, "requires 'from"},
		{`// @import { A } from "./a"`, "absolute Go import path"},
		{`// @import { A } from "example.com/a" trailing`, "unexpected trailing"},
	}
	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			_, ok, err := ParseImportComment(
				test.input,
				token.Position{Filename: "main.go", Line: 3, Column: 1},
			)
			if !ok || err == nil || !strings.Contains(err.Error(), test.message) {
				t.Fatalf("ParseImportComment() ok = %t, error = %v", ok, err)
			}
		})
	}
}

func TestParseImportCommentRejectsLegacyDirective(t *testing.T) {
	_, recognized, err := ParseImportComment(
		`// @spice.import { Application } from "example.com/spice/core"`,
		token.Position{Filename: "main.go", Line: 4, Column: 1},
	)
	if !recognized || !IsLegacyImportError(err) {
		t.Fatalf(
			"ParseImportComment() recognized = %t, error = %v",
			recognized,
			err,
		)
	}
	if !strings.Contains(err.Error(), "replace it with @import") {
		t.Fatalf("ParseImportComment() error = %v", err)
	}
}

func equalImport(left, right annotation.ImportDirective) bool {
	if left.Kind != right.Kind ||
		left.Package != right.Package ||
		left.Namespace != right.Namespace ||
		left.Position != right.Position ||
		left.PhysicalPosition != right.PhysicalPosition ||
		left.Raw != right.Raw ||
		len(left.Bindings) != len(right.Bindings) {
		return false
	}
	for index := range left.Bindings {
		if left.Bindings[index] != right.Bindings[index] {
			return false
		}
	}
	return true
}
