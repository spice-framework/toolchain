package generate

import (
	"go/token"
	"go/types"
	"testing"

	"github.com/StevenBuglione/spice/compiler/provider"
)

func TestImportAliasesIncludeFactoryOutputPackage(t *testing.T) {
	t.Parallel()

	outputPackage := types.NewPackage("example.com/framework/i18n", "i18n")
	output := types.NewPointer(types.NewNamed(
		types.NewTypeName(token.NoPos, outputPackage, "Catalog", nil),
		types.NewStruct(nil, nil),
		nil,
	))
	aliases := importAliases(
		[]provider.Provider{{
			PackagePath: "example.com/application/presentation",
			Output:      output,
		}},
		nil,
		nil,
		nil,
		nil,
		commandFeatures{},
	)
	if aliases[outputPackage.Path()] != "i18n" {
		t.Fatalf("output package alias = %q", aliases[outputPackage.Path()])
	}
}
