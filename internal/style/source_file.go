package style

import (
	"go/ast"
	"go/token"
)

type sourceFile struct {
	path      string
	relative  string
	lineCount int
	syntax    *ast.File
	typeSpecs []*ast.TypeSpec
	functions []*ast.FuncDecl
	variables []*ast.ValueSpec
	constants []*ast.ValueSpec
	fileSet   *token.FileSet
}
