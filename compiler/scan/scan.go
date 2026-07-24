// Package scan discovers Spice annotations in Go source files.
package scan

import (
	"fmt"
	"go/ast"
	goparser "go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"github.com/StevenBuglione/spice/annotation"
	annotationparser "github.com/StevenBuglione/spice/compiler/parser"
)

// Target identifies the Go declaration associated with an annotation.
type Target string

const (
	TargetPackage  Target = "package"
	TargetType     Target = "type"
	TargetFunction Target = "function"
	TargetMethod   Target = "method"
	TargetVariable Target = "variable"
	TargetConstant Target = "constant"
)

// Occurrence associates one annotation with a Go declaration.
type Occurrence struct {
	Annotation annotation.Annotation
	Target     Target
	Name       string
	File       string
}

// Result is the deterministic output of scanning a source tree.
type Result struct {
	Files       int
	Occurrences []Occurrence
}

// PathRoot converts common package patterns such as ./... into a filesystem root.
func PathRoot(input string) string {
	input = strings.TrimSpace(input)
	if input == "" || input == "./..." || input == "..." {
		return "."
	}
	return strings.TrimSuffix(input, "/...")
}

// Tree scans every non-test and test Go file under root, excluding generated
// dependency and repository-management directories.
func Tree(root string) (Result, error) {
	root = PathRoot(root)
	set := token.NewFileSet()
	result := Result{}

	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "vendor", "node_modules", ".spice":
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}

		file, err := goparser.ParseFile(set, path, nil, goparser.ParseComments)
		if err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		result.Files++

		if file.Doc != nil {
			occurrences, err := parseGroup(set, file.Doc, TargetPackage, file.Name.Name, path)
			if err != nil {
				return err
			}
			result.Occurrences = append(result.Occurrences, occurrences...)
		}

		for _, declaration := range file.Decls {
			switch node := declaration.(type) {
			case *ast.FuncDecl:
				if node.Doc == nil {
					continue
				}
				target := TargetFunction
				if node.Recv != nil {
					target = TargetMethod
				}
				occurrences, err := parseGroup(set, node.Doc, target, node.Name.Name, path)
				if err != nil {
					return err
				}
				result.Occurrences = append(result.Occurrences, occurrences...)
			case *ast.GenDecl:
				if node.Doc != nil {
					target := targetForToken(node.Tok)
					name := firstSpecName(node.Specs)
					occurrences, err := parseGroup(set, node.Doc, target, name, path)
					if err != nil {
						return err
					}
					result.Occurrences = append(result.Occurrences, occurrences...)
				}
				for _, spec := range node.Specs {
					specDoc, name := specDocumentation(spec)
					if specDoc == nil {
						continue
					}
					occurrences, err := parseGroup(set, specDoc, targetForToken(node.Tok), name, path)
					if err != nil {
						return err
					}
					result.Occurrences = append(result.Occurrences, occurrences...)
				}
			}
		}
		return nil
	})
	if err != nil {
		return Result{}, err
	}

	sort.Slice(result.Occurrences, func(i, j int) bool {
		left := result.Occurrences[i]
		right := result.Occurrences[j]
		if left.File != right.File {
			return left.File < right.File
		}
		if left.Annotation.Position.Line != right.Annotation.Position.Line {
			return left.Annotation.Position.Line < right.Annotation.Position.Line
		}
		return left.Annotation.Name < right.Annotation.Name
	})
	return result, nil
}

func parseGroup(set *token.FileSet, group *ast.CommentGroup, target Target, name, path string) ([]Occurrence, error) {
	var result []Occurrence
	for _, comment := range group.List {
		lines := strings.Split(comment.Text, "\n")
		for offset, line := range lines {
			position := set.Position(comment.Pos())
			position.Line += offset
			parsed, ok, err := annotationparser.ParseComment(line, position)
			if err != nil {
				return nil, err
			}
			if !ok {
				continue
			}
			result = append(result, Occurrence{
				Annotation: parsed,
				Target:     target,
				Name:       name,
				File:       path,
			})
		}
	}
	return result, nil
}

func targetForToken(value token.Token) Target {
	switch value {
	case token.TYPE:
		return TargetType
	case token.CONST:
		return TargetConstant
	default:
		return TargetVariable
	}
}

func firstSpecName(specs []ast.Spec) string {
	for _, spec := range specs {
		_, name := specDocumentation(spec)
		if name != "" {
			return name
		}
	}
	return "<declaration>"
}

func specDocumentation(spec ast.Spec) (*ast.CommentGroup, string) {
	switch node := spec.(type) {
	case *ast.TypeSpec:
		return node.Doc, node.Name.Name
	case *ast.ValueSpec:
		name := "<value>"
		if len(node.Names) > 0 {
			name = node.Names[0].Name
		}
		return node.Doc, name
	default:
		return nil, ""
	}
}
