package service

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestProductionCompilerHasNoBuiltInAnnotationSemantics(t *testing.T) {
	t.Parallel()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	for _, directory := range []string{"compiler", filepath.Join("internal", "cli")} {
		err := filepath.WalkDir(
			filepath.Join(root, directory),
			func(path string, entry fs.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if entry.IsDir() ||
					!strings.HasSuffix(entry.Name(), ".go") ||
					strings.HasSuffix(entry.Name(), "_test.go") {
					return nil
				}
				assertNoBuiltInAnnotationSemantics(t, path)
				return nil
			},
		)
		if err != nil {
			t.Fatalf("scan %s: %v", directory, err)
		}
	}
}

func assertNoBuiltInAnnotationSemantics(t *testing.T, path string) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(
		fileSet,
		path,
		content,
		parser.ImportsOnly,
	)
	if err != nil {
		t.Fatalf("parse imports in %s: %v", path, err)
	}
	for _, imported := range file.Imports {
		importPath, unquoteErr := strconv.Unquote(imported.Path.Value)
		if unquoteErr != nil {
			t.Fatalf("decode import in %s: %v", path, unquoteErr)
		}
		if importPath ==
			"github.com/spice-framework/spice/annotation/builtin" {
			t.Errorf(
				"%s imports the retired built-in annotation registry",
				path,
			)
		}
	}

	file, err = parser.ParseFile(fileSet, path, content, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	ast.Inspect(file, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.SwitchStmt:
			if annotationNameSelector(value.Tag) {
				t.Errorf(
					"%s:%d switches on Annotation.Name",
					path,
					fileSet.Position(value.Pos()).Line,
				)
			}
		case *ast.BinaryExpr:
			if value.Op != token.EQL && value.Op != token.NEQ {
				return true
			}
			if (annotationNameSelector(value.X) &&
				stringLiteral(value.Y)) ||
				(annotationNameSelector(value.Y) &&
					stringLiteral(value.X)) {
				t.Errorf(
					"%s:%d compares Annotation.Name",
					path,
					fileSet.Position(value.Pos()).Line,
				)
			}
		}
		return true
	})
}

func annotationNameSelector(expression ast.Expr) bool {
	name, ok := expression.(*ast.SelectorExpr)
	if !ok || name.Sel.Name != "Name" {
		return false
	}
	annotation, ok := name.X.(*ast.SelectorExpr)
	return ok && annotation.Sel.Name == "Annotation"
}

func stringLiteral(expression ast.Expr) bool {
	literal, ok := expression.(*ast.BasicLit)
	return ok && literal.Kind == token.STRING
}
