package contextbackground

import (
	"go/ast"
	"strings"

	"github.com/openshift/hypershift/hack/tools/hypershiftlinter/analyzers/pathutil"

	"golang.org/x/tools/go/analysis"
)

var Analyzer = &analysis.Analyzer{
	Name: "contextbackground",
	Doc:  "bans context.Background() and context.TODO() in test files; use tc.Context instead",
	Run:  run,
}

func run(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		filename := pass.Fset.File(file.Pos()).Name()
		if !pathutil.IsV2E2ETest(filename) || !strings.HasSuffix(filename, "_test.go") {
			continue
		}

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}

			if !isContextBackgroundOrTODO(call) {
				return true
			}

			if isInsideExemptFunc(file, call) {
				return true
			}

			pass.Report(analysis.Diagnostic{
				Pos:     call.Pos(),
				End:     call.End(),
				Message: "use tc.Context instead of context.Background()/context.TODO()",
			})
			return true
		})
	}
	return nil, nil
}

func isContextBackgroundOrTODO(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return ident.Name == "context" && (sel.Sel.Name == "Background" || sel.Sel.Name == "TODO")
}

func isInsideExemptFunc(file *ast.File, target *ast.CallExpr) bool {
	exempt := false
	ast.Inspect(file, func(n ast.Node) bool {
		if exempt {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		name := callName(call)
		if name == "BeforeSuite" || name == "DeferCleanup" || name == "SynchronizedBeforeSuite" || name == "SynchronizedAfterSuite" {
			for _, arg := range call.Args {
				if containsNode(arg, target) {
					exempt = true
					return false
				}
			}
		}
		return true
	})
	return exempt
}

func callName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		return fn.Sel.Name
	}
	return ""
}

func containsNode(tree ast.Node, target ast.Node) bool {
	found := false
	ast.Inspect(tree, func(n ast.Node) bool {
		if n == target {
			found = true
			return false
		}
		return !found
	})
	return found
}
