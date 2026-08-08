package ipv6url

import (
	"go/ast"
	"go/token"
	"regexp"
	"strconv"

	"golang.org/x/tools/go/analysis"

	"github.com/openshift/hypershift/hack/tools/hypershiftlinter/analyzers/pathutil"
)

var Analyzer = &analysis.Analyzer{
	Name: "ipv6url",
	Doc:  "detects fmt.Sprintf URL patterns that break with IPv6; use net.JoinHostPort instead",
	Run:  run,
}

var hostPortPattern = regexp.MustCompile(`%s:%[dv]`)

func run(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		filename := pass.Fset.File(file.Pos()).Name()
		if !pathutil.IsV2E2ETest(filename) {
			continue
		}

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}

			if !isFmtSprintf(call) {
				return true
			}

			formatLit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || formatLit.Kind != token.STRING {
				return true
			}

			formatStr, err := strconv.Unquote(formatLit.Value)
			if err != nil {
				return true
			}

			match := hostPortPattern.FindString(formatStr)
			if match != "" {
				pass.Report(analysis.Diagnostic{
					Pos:     call.Pos(),
					End:     call.End(),
					Message: "fmt.Sprintf with " + match + " may produce invalid URLs for IPv6 addresses; use net.JoinHostPort instead",
				})
			}

			return true
		})
	}
	return nil, nil
}

func isFmtSprintf(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return ident.Name == "fmt" && sel.Sel.Name == "Sprintf"
}
