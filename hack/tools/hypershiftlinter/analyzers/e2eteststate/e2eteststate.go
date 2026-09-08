package e2eteststate

import (
	"go/ast"
	"go/token"
	"strconv"
	"strings"

	"github.com/openshift/hypershift/hack/tools/hypershiftlinter/analyzers/pathutil"

	"golang.org/x/tools/go/analysis"
)

var Analyzer = &analysis.Analyzer{
	Name: "e2eteststate",
	Doc:  "requires every v2 e2e Ginkgo subject node to be explicitly labeled Informing or Blocking",
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

			name := callName(call)
			informing, blocking := testStates(call)
			if isContainer(name) {
				if informing || blocking || hasLiteralState(call) {
					pass.Reportf(call.Pos(), "internal.InformingLabel and internal.BlockingLabel may only be applied directly to Ginkgo subject nodes")
				}
				return true
			}
			if !isSubjectNode(name) {
				return true
			}
			if hasLiteralState(call) {
				pass.Reportf(call.Pos(), `Ginkgo subject state labels must use internal.InformingLabel or internal.BlockingLabel instead of string literals`)
				return true
			}

			switch {
			case informing && blocking:
				pass.Reportf(call.Pos(), "Ginkgo subject must not have both internal.InformingLabel and internal.BlockingLabel")
			case !informing && !blocking:
				pass.Reportf(call.Pos(), "Ginkgo subject must have Label(internal.InformingLabel) or Label(internal.BlockingLabel); new tests must be informing")
			}
			return true
		})
	}
	return nil, nil
}

func isSubjectNode(name string) bool {
	switch name {
	case "It", "FIt", "PIt", "XIt", "Specify", "FSpecify", "PSpecify", "XSpecify":
		return true
	default:
		return false
	}
}

func hasLiteralState(call *ast.CallExpr) bool {
	for _, arg := range call.Args {
		labelCall, ok := arg.(*ast.CallExpr)
		if !ok || callName(labelCall) != "Label" {
			continue
		}
		for _, label := range labelCall.Args {
			literal, ok := label.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				continue
			}
			value, err := strconv.Unquote(literal.Value)
			if err == nil && (value == "Informing" || value == "Blocking") {
				return true
			}
		}
	}
	return false
}

func isContainer(name string) bool {
	switch name {
	case "Describe", "FDescribe", "PDescribe", "XDescribe",
		"Context", "FContext", "PContext", "XContext",
		"When", "FWhen", "PWhen", "XWhen":
		return true
	default:
		return false
	}
}

func testStates(it *ast.CallExpr) (informing, blocking bool) {
	for _, arg := range it.Args {
		labelCall, ok := arg.(*ast.CallExpr)
		if !ok || callName(labelCall) != "Label" {
			continue
		}
		for _, label := range labelCall.Args {
			switch stateLabelName(label) {
			case "InformingLabel":
				informing = true
			case "BlockingLabel":
				blocking = true
			}
		}
	}
	return informing, blocking
}

func callName(call *ast.CallExpr) string {
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		return fn.Sel.Name
	default:
		return ""
	}
}

func stateLabelName(expr ast.Expr) string {
	selector, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	packageName, ok := selector.X.(*ast.Ident)
	if !ok || packageName.Name != "internal" {
		return ""
	}
	return selector.Sel.Name
}
