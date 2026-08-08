package testcasename

import (
	"go/ast"
	"go/token"
	"regexp"
	"strconv"

	"golang.org/x/tools/go/analysis"

	"github.com/openshift/hypershift/hack/tools/hypershiftlinter/analyzers/pathutil"
)

var Analyzer = &analysis.Analyzer{
	Name: "testcasename",
	Doc:  "checks that test case name fields match \"When <condition>, it should <expected behavior>\"",
	Run:  run,
}

var namePattern = regexp.MustCompile(`(?i)^when .+ should .+$`)

func run(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		filename := pass.Fset.File(file.Pos()).Name()
		if !pathutil.IsUnitTest(filename) {
			continue
		}

		ast.Inspect(file, func(n ast.Node) bool {
			comp, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}

			// Only check structs that appear to be test cases
			if !looksLikeTestCaseStruct(comp) {
				return true
			}

			for _, elt := range comp.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}

				ident, ok := kv.Key.(*ast.Ident)
				if !ok || ident.Name != "name" {
					continue
				}

				lit, ok := kv.Value.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}

				value, err := strconv.Unquote(lit.Value)
				if err != nil {
					continue
				}

				if !namePattern.MatchString(value) {
					pass.Report(analysis.Diagnostic{
						Pos:     lit.Pos(),
						End:     lit.End(),
						Message: `test case name "` + value + `" must match format "When <condition>, it should <expected behavior>"`,
					})
				}
			}
			return true
		})
	}
	return nil, nil
}

// looksLikeTestCaseStruct returns true if the composite literal appears to be
// a test case struct (has a "name" field plus other typical test fields).
func looksLikeTestCaseStruct(comp *ast.CompositeLit) bool {
	// Must be a struct literal (no type or anonymous struct type)
	if comp.Type != nil {
		if _, ok := comp.Type.(*ast.StructType); !ok {
			// Has a named type - not an anonymous test case struct
			return false
		}
	}

	hasName := false
	hasTestField := false

	for _, elt := range comp.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		ident, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}

		if ident.Name == "name" {
			hasName = true
		}

		// Common test case field names
		testFieldNames := []string{
			"want", "expected", "expectError", "expectErr", "wantErr",
			"args", "input", "output", "result", "fields", "setup",
			"assertion", "validate", "check",
		}
		for _, testField := range testFieldNames {
			if ident.Name == testField {
				hasTestField = true
				break
			}
		}

		if hasName && hasTestField {
			return true
		}
	}

	return false
}
