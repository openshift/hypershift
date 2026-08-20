package testfuncname

import (
	"go/ast"
	"strings"

	"github.com/openshift/hypershift/hack/tools/hypershiftlinter/analyzers/pathutil"

	"golang.org/x/tools/go/analysis"
)

var Analyzer = &analysis.Analyzer{
	Name: "testfuncname",
	Doc:  "checks that test functions do not use Test_ prefix; use TestFunctionName instead",
	Run:  run,
}

func run(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		filename := pass.Fset.File(file.Pos()).Name()
		if !pathutil.IsUnitTest(filename) {
			continue
		}

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil {
				continue
			}

			name := fn.Name.Name
			if strings.HasPrefix(name, "Test_") {
				pass.Report(analysis.Diagnostic{
					Pos:     fn.Name.Pos(),
					End:     fn.Name.End(),
					Message: `test function "` + name + `" must not use Test_ prefix; use Test` + strings.TrimPrefix(name, "Test_"),
				})
			}
		}
	}
	return nil, nil
}
