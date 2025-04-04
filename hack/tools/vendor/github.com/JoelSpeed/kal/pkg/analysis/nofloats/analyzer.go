package nofloats

import (
	"errors"
	"go/ast"

	"github.com/JoelSpeed/kal/pkg/analysis/utils"
	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

const name = "nofloats"

var (
	errCouldNotGetInspector = errors.New("could not get inspector")
)

// Analyzer is the analyzer for the nofloats package.
// It checks that no struct fields are `float`.
var Analyzer = &analysis.Analyzer{
	Name:     name,
	Doc:      "Float values cannot be reliably round-tripped without changing and have varying precisions and representations across languages and architectures.",
	Run:      run,
	Requires: []*analysis.Analyzer{inspect.Analyzer},
}

func run(pass *analysis.Pass) (interface{}, error) {
	inspect, ok := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	if !ok {
		return nil, errCouldNotGetInspector
	}

	// Filter to structs so that we can look at fields within structs.
	// Filter typespecs so that we can look at type aliases.
	nodeFilter := []ast.Node{
		(*ast.StructType)(nil),
		(*ast.TypeSpec)(nil),
	}

	typeChecker := utils.NewTypeChecker(checkFloat)

	// Preorder visits all the nodes of the AST in depth-first order. It calls
	// f(n) for each node n before it visits n's children.
	//
	// We use the filter defined above, ensuring we only look at struct fields and type declarations.
	inspect.Preorder(nodeFilter, func(n ast.Node) {
		typeChecker.CheckNode(pass, n)
	})

	return nil, nil //nolint:nilnil
}

func checkFloat(pass *analysis.Pass, ident *ast.Ident, node ast.Node, prefix string) {
	if ident.Name == "float32" || ident.Name == "float64" {
		pass.Reportf(node.Pos(), "%s should not use a float value because they cannot be reliably round-tripped.", prefix)
	}
}
