package e2eutilallowlist

import (
	"fmt"
	"go/ast"

	"github.com/openshift/hypershift/hack/tools/hypershiftlinter/analyzers/pathutil"

	"golang.org/x/tools/go/analysis"
)

var Analyzer = &analysis.Analyzer{
	Name: "e2eutilallowlist",
	Doc:  "forbids all references to test/e2e/util from test/e2e/v2; all v1 symbols have been ported to test/e2e/v2/util",
	Run:  run,
}

// allowlist is intentionally empty: all v1 e2e/util symbols have been ported
// into test/e2e/v2/util. No reference from v2 code to test/e2e/util is allowed.
var allowlist = map[string]map[string]bool{}

func run(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		filename := pass.Fset.File(file.Pos()).Name()
		if !pathutil.IsV2E2ETest(filename) {
			continue
		}

		ast.Inspect(file, func(n ast.Node) bool {
			ident, ok := n.(*ast.Ident)
			if !ok {
				return true
			}

			// Skip blank identifiers
			if ident.Name == "_" {
				return true
			}

			// Look up the resolved object for this identifier
			obj := pass.TypesInfo.Uses[ident]
			if obj == nil {
				return true
			}

			// Skip non-package-level objects
			if obj.Pkg() == nil {
				return true
			}

			pkgPath := obj.Pkg().Path()
			const utilPkgPath = "github.com/openshift/hypershift/test/e2e/util"
			if pkgPath != utilPkgPath {
				return true
			}

			// Check if this symbol is in the allowlist
			if isAllowed(pkgPath, obj.Name()) {
				return true
			}

			// Report diagnostic
			pass.Report(analysis.Diagnostic{
				Pos: ident.Pos(),
				End: ident.End(),
				Message: fmt.Sprintf(
					"reference to %s.%s is not allowed from test/e2e/v2; add it to the e2eutilallowlist allowlist or refactor to remove the dependency",
					pkgPath,
					obj.Name(),
				),
			})
			return true
		})
	}
	return nil, nil
}

// isAllowed checks whether a symbol is in the allowlist for its package.
func isAllowed(pkgPath string, symbolName string) bool {
	allowed, ok := allowlist[pkgPath]
	if !ok {
		return false
	}
	return allowed[symbolName]
}
