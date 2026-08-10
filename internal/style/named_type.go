package style

import (
	"go/ast"
	"go/types"
)

type namedType struct {
	name       string
	file       sourceFile
	spec       *ast.TypeSpec
	typeObject *types.TypeName
}
