package ipv6url

import (
	"go/ast"
	"go/token"
	"regexp"
	"strconv"

	"github.com/openshift/hypershift/hack/tools/hypershiftlinter/analyzers/pathutil"

	"golang.org/x/tools/go/analysis"
)

var Analyzer = &analysis.Analyzer{
	Name: "ipv6url",
	Doc:  "detects fmt.Sprintf URL patterns that break with IPv6; use net.JoinHostPort instead",
	Run:  run,
}

var hostPortPattern = regexp.MustCompile(`%s:%[dvs]`)

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

			funcName := fmtFormatFunc(call)
			if funcName == "" {
				return true
			}

			// Fprintf's first arg is io.Writer; the format string is the second arg.
			fmtIdx := 0
			if funcName == "Fprintf" {
				fmtIdx = 1
			}
			if len(call.Args) <= fmtIdx {
				return true
			}

			formatLit, ok := call.Args[fmtIdx].(*ast.BasicLit)
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
					Message: "fmt." + funcName + " with " + match + " may produce invalid URLs for IPv6 addresses; use net.JoinHostPort instead",
				})
			}

			return true
		})
	}
	return nil, nil
}

// fmtFormatFunc returns the function name (e.g. "Sprintf", "Errorf", "Fprintf")
// if the call is a fmt format function that could break with IPv6 addresses,
// or "" if it is not.
func fmtFormatFunc(call *ast.CallExpr) string {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return ""
	}
	if ident.Name != "fmt" {
		return ""
	}
	switch sel.Sel.Name {
	case "Sprintf", "Errorf", "Fprintf":
		return sel.Sel.Name
	default:
		return ""
	}
}
