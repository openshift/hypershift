package guestcluster

import (
	"go/ast"
	"go/token"
	"strconv"
	"strings"

	"github.com/openshift/hypershift/hack/tools/hypershiftlinter/analyzers/pathutil"

	"golang.org/x/tools/go/analysis"
)

var Analyzer = &analysis.Analyzer{
	Name: "guestcluster",
	Doc:  `bans "guest cluster" terminology; use "hosted cluster" instead`,
	Run:  run,
}

func run(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		filename := pass.Fset.File(file.Pos()).Name()
		if !pathutil.IsV2E2ETest(filename) {
			continue
		}

		for _, cg := range file.Comments {
			for _, comment := range cg.List {
				if containsGuestCluster(comment.Text) {
					pass.Report(analysis.Diagnostic{
						Pos:     comment.Pos(),
						End:     comment.End(),
						Message: `use "hosted cluster" instead of "guest cluster"`,
					})
				}
			}
		}

		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			if containsGuestCluster(value) {
				pass.Report(analysis.Diagnostic{
					Pos:     lit.Pos(),
					End:     lit.End(),
					Message: `use "hosted cluster" instead of "guest cluster"`,
				})
			}
			return true
		})
	}
	return nil, nil
}

func containsGuestCluster(s string) bool {
	lower := strings.ToLower(s)
	if strings.Contains(lower, "guest cluster") {
		return true
	}
	if strings.Contains(s, "guestCluster") || strings.Contains(s, "GuestCluster") || strings.Contains(s, "guestcluster") {
		return true
	}
	return false
}
