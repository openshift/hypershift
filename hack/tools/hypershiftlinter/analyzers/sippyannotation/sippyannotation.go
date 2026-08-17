package sippyannotation

import (
	"go/ast"
	"go/token"
	"strconv"
	"strings"

	"github.com/openshift/hypershift/hack/tools/hypershiftlinter/analyzers/pathutil"

	"golang.org/x/tools/go/analysis"
)

var Analyzer = &analysis.Analyzer{
	Name: "sippyannotation",
	Doc:  "checks that Ginkgo Describe blocks have [sig-hypershift][Jira:Hypershift] prefix and [Feature:X] annotation",
	Run:  run,
}

const requiredPrefix = "[sig-hypershift][Jira:Hypershift]"

func run(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		filename := pass.Fset.File(file.Pos()).Name()
		if !pathutil.IsV2E2ETest(filename) {
			continue
		}

		ast.Inspect(file, func(n ast.Node) bool {
			call := extractDescribeCall(n)
			if call == nil {
				return true
			}

			name, namePos := getFirstStringArg(call)
			if name == "" {
				return true
			}

			if !strings.HasPrefix(name, requiredPrefix) {
				pass.Report(analysis.Diagnostic{
					Pos:     namePos,
					Message: `Describe block must start with [sig-hypershift][Jira:Hypershift]`,
				})
				return false
			}

			if !hasValidFeatureAnnotation(name) {
				if !hasFeatureInChildren(call) {
					pass.Report(analysis.Diagnostic{
						Pos:     namePos,
						Message: `Describe block has no [Feature:X] annotation — add to Describe or to every child Context/When`,
					})
				}
			}

			return false
		})
	}
	return nil, nil
}

func extractDescribeCall(n ast.Node) *ast.CallExpr {
	switch node := n.(type) {
	case *ast.AssignStmt:
		if len(node.Rhs) == 1 {
			if call, ok := node.Rhs[0].(*ast.CallExpr); ok && isGinkgoCall(call, "Describe") {
				return call
			}
		}
	case *ast.ValueSpec:
		if len(node.Values) == 1 {
			if call, ok := node.Values[0].(*ast.CallExpr); ok && isGinkgoCall(call, "Describe") {
				return call
			}
		}
	case *ast.ExprStmt:
		if call, ok := node.X.(*ast.CallExpr); ok && isGinkgoCall(call, "Describe") {
			return call
		}
	}
	return nil
}

func isGinkgoCall(call *ast.CallExpr, name string) bool {
	switch fun := call.Fun.(type) {
	case *ast.Ident:
		return fun.Name == name
	case *ast.SelectorExpr:
		return fun.Sel.Name == name
	}
	return false
}

func getFirstStringArg(call *ast.CallExpr) (string, token.Pos) {
	if len(call.Args) == 0 {
		return "", token.NoPos
	}
	lit, ok := call.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", token.NoPos
	}
	value, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", token.NoPos
	}
	return value, lit.Pos()
}

func hasValidFeatureAnnotation(name string) bool {
	idx := strings.Index(name, "[Feature:")
	if idx == -1 {
		return false
	}
	rest := name[idx+len("[Feature:"):]
	end := strings.Index(rest, "]")
	return end > 0
}

func hasFeatureInChildren(call *ast.CallExpr) bool {
	found := false
	for _, arg := range call.Args {
		ast.Inspect(arg, func(n ast.Node) bool {
			if found {
				return false
			}
			innerCall, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if isGinkgoCall(innerCall, "Context") || isGinkgoCall(innerCall, "When") {
				name, _ := getFirstStringArg(innerCall)
				if hasValidFeatureAnnotation(name) {
					found = true
					return false
				}
			}
			// Register*Tests functions register Context/When blocks with Features indirectly
			if ident, ok := innerCall.Fun.(*ast.Ident); ok && strings.HasPrefix(ident.Name, "Register") && strings.HasSuffix(ident.Name, "Tests") {
				found = true
				return false
			}
			return true
		})
	}
	return found
}
