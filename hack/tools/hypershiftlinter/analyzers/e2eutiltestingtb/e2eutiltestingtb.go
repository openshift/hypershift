package e2eutiltestingtb

import (
	"go/ast"
	"go/types"
	"strings"

	"github.com/openshift/hypershift/hack/tools/hypershiftlinter/analyzers/pathutil"

	"golang.org/x/tools/go/analysis"
)

const e2eUtilPackagePath = "github.com/openshift/hypershift/test/e2e/util"

var Analyzer = &analysis.Analyzer{
	Name: "e2eutiltestingtb",
	Doc:  "forbids test/e2e/util symbols whose function signature accepts testing.TB from test/e2e/v2",
	Run:  run,
}

func run(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		filename := pass.Fset.File(file.Pos()).Name()
		if !pathutil.IsV2E2ETest(filename) {
			continue
		}

		ast.Inspect(file, func(n ast.Node) bool {
			ident, ok := n.(*ast.Ident)
			if !ok || ident.Name == "_" {
				return true
			}

			obj := pass.TypesInfo.Uses[ident]
			if obj == nil || obj.Pkg() == nil {
				return true
			}
			pkgPath := obj.Pkg().Path()
			if pkgPath != e2eUtilPackagePath && !strings.HasPrefix(pkgPath, e2eUtilPackagePath+"/") {
				return true
			}
			if !acceptsTestingTB(obj) {
				return true
			}

			pass.Report(analysis.Diagnostic{
				Pos: ident.Pos(),
				End: ident.End(),
				Message: "reference to " + pkgPath + "." + obj.Name() +
					" is forbidden from test/e2e/v2: its function signature accepts testing.TB; port it to a v2 error-returning helper",
			})
			return true
		})
	}
	return nil, nil
}

func acceptsTestingTB(obj types.Object) bool {
	sig := functionSignature(obj.Type())
	if sig == nil {
		return false
	}
	for i := 0; i < sig.Params().Len(); i++ {
		if containsTestingTB(sig.Params().At(i).Type()) {
			return true
		}
	}
	return false
}

func functionSignature(typ types.Type) *types.Signature {
	typ = types.Unalias(typ)
	switch typ := typ.(type) {
	case *types.Signature:
		return typ
	case *types.Named:
		return functionSignature(typ.Underlying())
	default:
		return nil
	}
}

func containsTestingTB(typ types.Type) bool {
	typ = types.Unalias(typ)
	switch typ := typ.(type) {
	case *types.Named:
		obj := typ.Obj()
		return obj.Pkg() != nil && obj.Pkg().Path() == "testing" && (obj.Name() == "TB" || obj.Name() == "T")
	case *types.Pointer:
		return containsTestingTB(typ.Elem())
	case *types.Slice:
		return containsTestingTB(typ.Elem())
	default:
		return false
	}
}
