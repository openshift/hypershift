package vacuouspass

import (
	"go/ast"
	"go/token"
	"slices"
	"strconv"

	"github.com/openshift/hypershift/hack/tools/hypershiftlinter/analyzers/pathutil"

	"golang.org/x/tools/go/analysis"
)

var Analyzer = &analysis.Analyzer{
	Name: "vacuouspass",
	Doc:  "detects range over .Items without preceding non-empty assertion (vacuous pass)",
	Run:  run,
}

var ginkgoContainers = map[string]bool{
	"Describe": true, "Context": true, "When": true,
}

var ginkgoAll = map[string]bool{
	"Describe": true, "Context": true, "When": true,
	"It": true, "BeforeEach": true,
}

func run(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		filename := pass.Fset.File(file.Pos()).Name()
		if !pathutil.IsV2E2ETest(filename) {
			continue
		}

		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Body != nil {
					walkGinkgoBlock(pass, d.Body, nil)
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok {
						continue
					}
					for _, val := range vs.Values {
						call, ok := val.(*ast.CallExpr)
						if !ok {
							continue
						}
						if isGinkgoCall(call, ginkgoContainers) {
							body := getClosureBody(call)
							if body != nil {
								walkGinkgoBlock(pass, body, nil)
							}
						}
					}
				}
			}
		}
	}
	return nil, nil
}

// walkGinkgoBlock walks a block that may contain Ginkgo containers and leaves.
// It collects BeforeEach assertions and passes them to child It blocks.
// For non-Ginkgo statements, it checks range loops directly.
func walkGinkgoBlock(pass *analysis.Pass, body *ast.BlockStmt, beforeEachAssertions []string) {
	localAssertions := collectBeforeEachAssertions(body)
	merged := append(beforeEachAssertions, localAssertions...)

	for _, stmt := range body.List {
		call := extractCallByName(stmt, ginkgoAll)
		if call != nil {
			name := callName(call)
			closureBody := getClosureBody(call)
			if closureBody == nil {
				continue
			}
			if ginkgoContainers[name] {
				walkGinkgoBlock(pass, closureBody, merged)
			} else if name == "It" {
				checkBlockForVacuousPass(pass, closureBody, merged)
			}
			continue
		}

		// For non-Ginkgo statements in function bodies (e.g., helper functions
		// that contain range loops), check them directly.
		checkStmtForVacuousPass(pass, stmt, body, merged)
	}
}

// checkBlockForVacuousPass checks all range-over-.Items loops in a block.
func checkBlockForVacuousPass(pass *analysis.Pass, body *ast.BlockStmt, beforeEachAssertions []string) {
	for i, stmt := range body.List {
		rangeStmt, ok := stmt.(*ast.RangeStmt)
		if !ok {
			continue
		}

		sel, ok := rangeStmt.X.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Items" {
			continue
		}

		if !bodyContainsExpect(rangeStmt.Body) {
			continue
		}

		if hasBeEmptyAssertionBefore(body.List[:i], sel) {
			continue
		}

		targetStr := nodeString(sel)
		if containsString(beforeEachAssertions, targetStr) {
			continue
		}

		pass.Report(analysis.Diagnostic{
			Pos:     rangeStmt.Pos(),
			Message: "range over .Items without preceding non-empty assertion — add Expect(x.Items).NotTo(BeEmpty()) before the loop",
		})
	}
}

// checkStmtForVacuousPass checks a single non-Ginkgo statement for range loops.
func checkStmtForVacuousPass(pass *analysis.Pass, stmt ast.Stmt, parent *ast.BlockStmt, beforeEachAssertions []string) {
	rangeStmt, ok := stmt.(*ast.RangeStmt)
	if !ok {
		return
	}

	sel, ok := rangeStmt.X.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Items" {
		return
	}

	if !bodyContainsExpect(rangeStmt.Body) {
		return
	}

	idx := stmtIndex(parent, stmt)
	if idx > 0 && hasBeEmptyAssertionBefore(parent.List[:idx], sel) {
		return
	}

	targetStr := nodeString(sel)
	if containsString(beforeEachAssertions, targetStr) {
		return
	}

	pass.Report(analysis.Diagnostic{
		Pos:     rangeStmt.Pos(),
		Message: "range over .Items without preceding non-empty assertion — add Expect(x.Items).NotTo(BeEmpty()) before the loop",
	})
}

func stmtIndex(block *ast.BlockStmt, target ast.Stmt) int {
	for i, s := range block.List {
		if s == target {
			return i
		}
	}
	return -1
}

func isGinkgoCall(call *ast.CallExpr, names map[string]bool) bool {
	ident, ok := call.Fun.(*ast.Ident)
	return ok && names[ident.Name]
}

func callName(call *ast.CallExpr) string {
	if ident, ok := call.Fun.(*ast.Ident); ok {
		return ident.Name
	}
	return ""
}

func extractCallByName(stmt ast.Stmt, names map[string]bool) *ast.CallExpr {
	exprStmt, ok := stmt.(*ast.ExprStmt)
	if !ok {
		return nil
	}
	call, ok := exprStmt.X.(*ast.CallExpr)
	if !ok {
		return nil
	}
	if isGinkgoCall(call, names) {
		return call
	}
	return nil
}

// getClosureBody returns the body of the last FuncLit argument in a call.
func getClosureBody(call *ast.CallExpr) *ast.BlockStmt {
	for i := len(call.Args) - 1; i >= 0; i-- {
		if fn, ok := call.Args[i].(*ast.FuncLit); ok {
			return fn.Body
		}
	}
	return nil
}

// collectBeforeEachAssertions finds all BeforeEach calls in a block and
// returns the target strings of any non-empty guard assertions.
func collectBeforeEachAssertions(body *ast.BlockStmt) []string {
	var targets []string
	for _, stmt := range body.List {
		exprStmt, ok := stmt.(*ast.ExprStmt)
		if !ok {
			continue
		}
		call, ok := exprStmt.X.(*ast.CallExpr)
		if !ok {
			continue
		}
		ident, ok := call.Fun.(*ast.Ident)
		if !ok || ident.Name != "BeforeEach" {
			continue
		}
		closureBody := getClosureBody(call)
		if closureBody == nil {
			continue
		}
		ast.Inspect(closureBody, func(n ast.Node) bool {
			c, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			target := extractNonEmptyGuardTarget(c)
			if target != "" {
				targets = append(targets, target)
			}
			return true
		})
	}
	return targets
}

// extractNonEmptyGuardTarget returns the target string if call is a
// non-empty assertion for .Items, otherwise "".
//
// Recognized patterns (only when the matcher literally proves length > 0):
//   - Expect(target).NotTo(BeEmpty())            (and ShouldNot / ToNot)
//   - Expect(target).To(HaveLen(n))              (n a positive integer literal)
//   - Expect(len(target)).To(BeNumerically(op, n)) where op/n prove length > 0
//
// Matchers that permit an empty collection — e.g. HaveLen(0),
// BeNumerically("<", 1), BeNumerically("==", 0) — are intentionally NOT treated
// as non-empty guards, otherwise a range over the collection would still be a
// vacuous pass.
func extractNonEmptyGuardTarget(call *ast.CallExpr) string {
	var (
		hasExpect      bool
		hasNegation    bool // NotTo, ShouldNot, ToNot
		hasPositive    bool // To, Should
		hasBeEmpty     bool
		provesNonEmpty bool // HaveLen/BeNumerically matcher that proves length > 0
		target         string
	)

	ast.Inspect(call, func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fn := c.Fun.(type) {
		case *ast.Ident:
			if fn.Name == "Expect" && len(c.Args) > 0 {
				hasExpect = true
				// Handle Expect(len(x.Items)) — unwrap the len() call
				if lenCall, ok := c.Args[0].(*ast.CallExpr); ok {
					if lenIdent, ok := lenCall.Fun.(*ast.Ident); ok && lenIdent.Name == "len" && len(lenCall.Args) > 0 {
						target = nodeString(lenCall.Args[0])
						return true
					}
				}
				target = nodeString(c.Args[0])
			}
			if fn.Name == "BeEmpty" {
				hasBeEmpty = true
			}
			if fn.Name == "HaveLen" && haveLenProvesNonEmpty(c) {
				provesNonEmpty = true
			}
			if fn.Name == "BeNumerically" && beNumericallyProvesNonEmpty(c) {
				provesNonEmpty = true
			}
		case *ast.SelectorExpr:
			switch fn.Sel.Name {
			case "NotTo", "ShouldNot", "ToNot":
				hasNegation = true
			case "To", "Should":
				hasPositive = true
			}
		}
		return true
	})

	if !hasExpect || target == "" {
		return ""
	}

	// Expect(x).NotTo(BeEmpty()) / ShouldNot / ToNot
	if hasNegation && hasBeEmpty {
		return target
	}
	// Expect(x).To(HaveLen(n>0)) or Expect(len(x)).To(BeNumerically(op, n)) that
	// proves a positive length.
	if hasPositive && provesNonEmpty {
		return target
	}

	return ""
}

// haveLenProvesNonEmpty reports whether a HaveLen(...) matcher call proves the
// collection has a positive length, i.e. the argument is a positive integer
// literal. HaveLen(0) does not qualify.
func haveLenProvesNonEmpty(call *ast.CallExpr) bool {
	if len(call.Args) != 1 {
		return false
	}
	n, ok := intLiteralValue(call.Args[0])
	return ok && n > 0
}

// beNumericallyProvesNonEmpty reports whether a BeNumerically(op, n) matcher
// call proves a positive length. Only comparisons with integer-literal bounds
// that guarantee length > 0 qualify:
//
//	">", 0    (or any n >= 0)     -> length strictly greater than n >= 0
//	">=", 1   (or any n >= 1)     -> length at least n >= 1
//	"==", n   with n >= 1         -> length exactly n >= 1
func beNumericallyProvesNonEmpty(call *ast.CallExpr) bool {
	if len(call.Args) != 2 {
		return false
	}
	op, ok := stringLiteralValue(call.Args[0])
	if !ok {
		return false
	}
	n, ok := intLiteralValue(call.Args[1])
	if !ok {
		return false
	}
	switch op {
	case ">":
		return n >= 0
	case ">=":
		return n >= 1
	case "==":
		return n >= 1
	default:
		return false
	}
}

// intLiteralValue returns the integer value of an integer BasicLit expression.
func intLiteralValue(expr ast.Expr) (int, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.INT {
		return 0, false
	}
	v, err := strconv.Atoi(lit.Value)
	if err != nil {
		return 0, false
	}
	return v, true
}

// stringLiteralValue returns the unquoted value of a string BasicLit expression.
func stringLiteralValue(expr ast.Expr) (string, bool) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	v, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return v, true
}

func hasBeEmptyAssertionBefore(stmts []ast.Stmt, target *ast.SelectorExpr) bool {
	targetStr := nodeString(target)
	for _, stmt := range stmts {
		found := false
		ast.Inspect(stmt, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if extractNonEmptyGuardTarget(call) == targetStr {
				found = true
				return false
			}
			return true
		})
		if found {
			return true
		}
	}
	return false
}

func bodyContainsExpect(body *ast.BlockStmt) bool {
	if body == nil {
		return false
	}
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "Expect" {
			found = true
			return false
		}
		return true
	})
	return found
}

func nodeString(n ast.Node) string {
	switch x := n.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		base := nodeString(x.X)
		if base != "" {
			return base + "." + x.Sel.Name
		}
		return x.Sel.Name
	}
	return ""
}

func containsString(slice []string, s string) bool {
	return slices.Contains(slice, s)
}
