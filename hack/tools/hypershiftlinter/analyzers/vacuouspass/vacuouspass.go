package vacuouspass

import (
	"go/ast"
	"slices"

	"golang.org/x/tools/go/analysis"

	"github.com/openshift/hypershift/hack/tools/hypershiftlinter/analyzers/pathutil"
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
// returns the target strings of any Expect(...).NotTo(BeEmpty()) assertions.
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
			target := extractBeEmptyTarget(c)
			if target != "" {
				targets = append(targets, target)
			}
			return true
		})
	}
	return targets
}

// extractBeEmptyTarget returns the target string if call is
// Expect(target).NotTo(BeEmpty()) or equivalent, otherwise "".
func extractBeEmptyTarget(call *ast.CallExpr) string {
	hasExpect := false
	hasNotTo := false
	hasBeEmpty := false
	var target string

	ast.Inspect(call, func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fn := c.Fun.(type) {
		case *ast.Ident:
			if fn.Name == "Expect" && len(c.Args) > 0 {
				hasExpect = true
				target = nodeString(c.Args[0])
			}
			if fn.Name == "BeEmpty" {
				hasBeEmpty = true
			}
		case *ast.SelectorExpr:
			if fn.Sel.Name == "NotTo" || fn.Sel.Name == "ShouldNot" || fn.Sel.Name == "ToNot" {
				hasNotTo = true
			}
		}
		return true
	})

	if hasExpect && hasNotTo && hasBeEmpty {
		return target
	}
	return ""
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
			if extractBeEmptyTarget(call) == targetStr {
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
