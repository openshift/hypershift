package e2eutilallowlist

import (
	"fmt"
	"go/ast"
	"go/types"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/openshift/hypershift/hack/tools/hypershiftlinter/analyzers/pathutil"

	"golang.org/x/tools/go/analysis"
)

const utilPkgPath = "github.com/openshift/hypershift/test/e2e/util"

var Analyzer = &analysis.Analyzer{
	Name: "e2eutilallowlist",
	Doc:  "denies legacy e2e utilities in v2 except for explicitly grandfathered function references",
	Run:  run,
}

func run(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		filename := filepath.ToSlash(pass.Fset.File(file.Pos()).Name())
		if !pathutil.IsV2E2ETest(filename) {
			continue
		}
		filename = filename[strings.LastIndex(filename, "test/e2e/v2/"):]

		// Nested closures inherit their enclosing function's allowance. Top-level
		// Ginkgo registrations use their exact Describe label as the function name.
		var scopes []string
		ast.Inspect(file, func(n ast.Node) bool {
			// Every non-nil visit must return true to balance the scope stack.
			if n == nil {
				scopes = scopes[:len(scopes)-1]
				return false
			}
			scope := ""
			if len(scopes) > 0 {
				scope = scopes[len(scopes)-1]
			}
			switch node := n.(type) {
			case *ast.ImportSpec:
				if node.Name != nil && node.Name.Name == "_" {
					pkgPath, err := strconv.Unquote(node.Path.Value)
					if err == nil && (pkgPath == utilPkgPath || strings.HasPrefix(pkgPath, utilPkgPath+"/")) {
						pass.Reportf(node.Pos(), "side-effect import of %s is not allowed from test/e2e/v2; remove the legacy utility dependency", pkgPath)
					}
				}
			case *ast.FuncDecl:
				scope = node.Name.Name
				if node.Recv != nil {
					fn := pass.TypesInfo.Defs[node.Name].(*types.Func)
					recv := fn.Type().(*types.Signature).Recv().Type()
					if ptr, ok := recv.(*types.Pointer); ok {
						recv = ptr.Elem()
					}
					scope = types.TypeString(recv, func(*types.Package) string { return "" }) + "." + scope
				}
			case *ast.CallExpr:
				if scope == "" {
					if name, ok := node.Fun.(*ast.Ident); ok && name.Name == "Describe" && len(node.Args) > 0 {
						if label, ok := node.Args[0].(*ast.BasicLit); ok {
							if value, err := strconv.Unquote(label.Value); err == nil {
								scope = "Describe(" + strconv.Quote(value) + ")"
							}
						}
					}
				}
			}
			scopes = append(scopes, scope)

			ident, ok := n.(*ast.Ident)
			if !ok {
				return true
			}
			obj := pass.TypesInfo.Uses[ident]
			if obj == nil || obj.Pkg() == nil {
				return true
			}
			// Package names are imports, not references to utility code.
			if _, ok := obj.(*types.PkgName); ok {
				return true
			}
			pkgPath := obj.Pkg().Path()
			if pkgPath != utilPkgPath && !strings.HasPrefix(pkgPath, utilPkgPath+"/") {
				return true
			}
			symbol := obj.Name()
			if pkgPath != utilPkgPath {
				symbol = strings.TrimPrefix(pkgPath, utilPkgPath+"/") + "." + symbol
			}
			if isAllowed(filename, scope, symbol) {
				return true
			}
			if scope == "" {
				scope = "package scope"
			}
			pass.Report(analysis.Diagnostic{
				Pos: ident.Pos(), End: ident.End(),
				Message: fmt.Sprintf("reference to %s.%s is not allowed from test/e2e/v2 in %s; refactor to remove the legacy utility dependency", pkgPath, obj.Name(), scope),
			})
			return true
		})
	}
	return nil, nil
}

// isAllowed requires an exact file, enclosing function, and utility symbol.
// There are no package-wide, file-wide, or symbol-prefix exceptions.
func isAllowed(filename, function, symbol string) bool {
	return slices.Contains(allowlist[filename][function], symbol)
}
