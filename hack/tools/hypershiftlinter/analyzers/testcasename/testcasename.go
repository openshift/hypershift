package testcasename

import (
	"go/ast"
	"go/token"
	"regexp"
	"slices"
	"strconv"

	"github.com/openshift/hypershift/hack/tools/hypershiftlinter/analyzers/pathutil"

	"golang.org/x/tools/go/analysis"
)

var Analyzer = &analysis.Analyzer{
	Name: "testcasename",
	Doc:  "checks that test case name fields match \"When <condition>, it should <expected behavior>\"",
	Run:  run,
}

var namePattern = regexp.MustCompile(`(?i)^when .+,? it should .+$`)

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

			// Check map-based test tables: map[string]struct{...}{ "name": {...}, ... }
			if looksLikeTestCaseMap(comp) {
				for _, elt := range comp.Elts {
					kv, ok := elt.(*ast.KeyValueExpr)
					if !ok {
						continue
					}

					lit, ok := kv.Key.(*ast.BasicLit)
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

		if isTestFieldName(ident.Name) {
			hasTestField = true
		}

		if hasName && hasTestField {
			return true
		}
	}

	return false
}

// looksLikeTestCaseMap returns true if the composite literal is a
// map[string]struct{...}{...} whose value struct declares at least one
// recognized test field — a common pattern for map-based test tables where the
// map keys serve as test case names. Requiring a recognized test field avoids
// flagging plain lookup/fixture maps such as
// map[string]struct{ Addr string }{...}.
func looksLikeTestCaseMap(comp *ast.CompositeLit) bool {
	mt, ok := comp.Type.(*ast.MapType)
	if !ok {
		return false
	}

	// Key type must be string.
	keyIdent, ok := mt.Key.(*ast.Ident)
	if !ok || keyIdent.Name != "string" {
		return false
	}

	// Value type must be a struct with at least one recognized test field.
	st, ok := mt.Value.(*ast.StructType)
	if !ok {
		return false
	}

	return structHasTestField(st)
}

// structHasTestField reports whether the struct type declares at least one
// field whose name is a recognized test field.
func structHasTestField(st *ast.StructType) bool {
	if st.Fields == nil {
		return false
	}
	for _, field := range st.Fields.List {
		for _, name := range field.Names {
			if isTestFieldName(name.Name) {
				return true
			}
		}
	}
	return false
}

// isTestFieldName reports whether name is one of the recognized field names
// that signal a struct is a test case rather than an arbitrary data struct.
func isTestFieldName(name string) bool {
	testFieldNames := []string{
		"want", "expected", "expectError", "expectErr", "wantErr",
		"args", "input", "output", "result", "fields", "setup",
		"assertion", "validate", "check",
	}
	return slices.Contains(testFieldNames, name)
}
