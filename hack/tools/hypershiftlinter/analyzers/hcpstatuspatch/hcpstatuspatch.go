// Package hcpstatuspatch bans direct Status().Update() calls and unguarded
// client.MergeFrom() status patches on HostedControlPlane. Multiple controllers
// (CPO, HCCO, HO/karpenter) write to the same HostedControlPlane.Status
// concurrently — Status().Update() replaces the whole status subresource and
// silently overwrites concurrent changes, and MergeFrom() without an optimistic
// lock lets a stale resourceVersion succeed silently. See
// support/statuspatching for the safe pattern.
package hcpstatuspatch

import (
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
)

const (
	controllerRuntimeClientPkg = "sigs.k8s.io/controller-runtime/pkg/client"
	hostedControlPlanePkg      = "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hostedControlPlaneType     = "HostedControlPlane"
	optimisticLockType         = "MergeFromWithOptimisticLock"
)

var Analyzer = &analysis.Analyzer{
	Name: "hcpstatuspatch",
	Doc:  "bans Status().Update() and unguarded MergeFrom() status patches on HostedControlPlane; use support/statuspatching instead",
	Run:  run,
}

func run(pass *analysis.Pass) (any, error) {
	for _, file := range pass.Files {
		filename := pass.Fset.File(file.Pos()).Name()
		// Test files legitimately seed fixture status via a fake client's
		// Status().Update() — there's no concurrent writer to race with in a
		// synchronous unit test, so the rule doesn't apply there.
		if strings.HasSuffix(filename, "_test.go") {
			continue
		}

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if isHCPStatusUpdate(pass, call) {
				pass.Report(analysis.Diagnostic{
					Pos:     call.Pos(),
					End:     call.End(),
					Message: "do not call Status().Update() on HostedControlPlane; use statuspatching.PatchStatus instead — Update() replaces the whole status subresource and silently overwrites concurrent writers",
				})
			}
			if isHCPStatusPatch(pass, call) {
				reportUnguardedPatchConstructors(pass, call)
			}
			return true
		})
	}
	return nil, nil
}

// isHCPStatusUpdate matches `<expr>.Status().Update(ctx, obj, ...)` where obj is
// a HyperShift HostedControlPlane.
func isHCPStatusUpdate(pass *analysis.Pass, call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Update" {
		return false
	}
	statusCall, ok := sel.X.(*ast.CallExpr)
	if !ok {
		return false
	}
	statusSel, ok := statusCall.Fun.(*ast.SelectorExpr)
	if !ok || statusSel.Sel.Name != "Status" {
		return false
	}
	if len(call.Args) < 2 {
		return false
	}
	return isHostedControlPlane(pass.TypesInfo.TypeOf(call.Args[1]))
}

// isHCPStatusPatch matches `<expr>.Status().Patch(ctx, obj, patch)` where obj is
// a HyperShift HostedControlPlane.
func isHCPStatusPatch(pass *analysis.Pass, call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Patch" {
		return false
	}
	statusCall, ok := sel.X.(*ast.CallExpr)
	if !ok {
		return false
	}
	statusSel, ok := statusCall.Fun.(*ast.SelectorExpr)
	if !ok || statusSel.Sel.Name != "Status" {
		return false
	}
	if len(call.Args) < 2 {
		return false
	}
	return isHostedControlPlane(pass.TypesInfo.TypeOf(call.Args[1]))
}

func reportUnguardedPatchConstructors(pass *analysis.Pass, statusPatch *ast.CallExpr) {
	if len(statusPatch.Args) < 3 {
		return
	}
	var enclosingFn *ast.FuncDecl
	for _, file := range pass.Files {
		if file.Pos() <= statusPatch.Pos() && statusPatch.Pos() < file.End() {
			enclosingFn = enclosingFuncDecl(file, statusPatch.Pos())
			break
		}
	}
	for _, call := range mergeCallsForPatchExpr(pass, statusPatch.Args[2], enclosingFn, statusPatch.Pos()) {
		name, isControllerRuntime := mergePatchConstructorName(pass, call)
		if !isControllerRuntime {
			continue
		}
		if len(call.Args) < 1 || !isHostedControlPlane(pass.TypesInfo.TypeOf(call.Args[0])) {
			continue
		}
		switch name {
		case "MergeFrom":
			pass.Report(analysis.Diagnostic{
				Pos:     call.Pos(),
				End:     call.End(),
				Message: "do not use MergeFrom() on HostedControlPlane without an optimistic lock; use statuspatching.PatchStatus/PatchStatusCondition, or MergeFromWithOptions(..., MergeFromWithOptimisticLock{}) at minimum",
			})
		case "MergeFromWithOptions":
			if !hasOptimisticLockOption(pass, call) {
				pass.Report(analysis.Diagnostic{
					Pos:     call.Pos(),
					End:     call.End(),
					Message: "do not use MergeFromWithOptions() on HostedControlPlane without MergeFromWithOptimisticLock{}; use statuspatching.PatchStatus/PatchStatusCondition instead",
				})
			}
		}
	}
}

func enclosingFuncDecl(file *ast.File, pos token.Pos) *ast.FuncDecl {
	var found *ast.FuncDecl
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok {
			return true
		}
		if fn.Pos() <= pos && pos < fn.End() {
			found = fn
		}
		return true
	})
	return found
}

func mergeCallsForPatchExpr(pass *analysis.Pass, patchExpr ast.Expr, enclosingFn *ast.FuncDecl, patchPos token.Pos) []*ast.CallExpr {
	switch expr := patchExpr.(type) {
	case *ast.CallExpr:
		return collectMergePatchConstructorCalls(pass, expr)
	case *ast.Ident:
		if enclosingFn == nil || enclosingFn.Body == nil {
			return nil
		}
		return findMergeAssignments(pass, expr, enclosingFn.Body, patchPos)
	default:
		return nil
	}
}

func collectMergePatchConstructorCalls(pass *analysis.Pass, expr ast.Expr) []*ast.CallExpr {
	var calls []*ast.CallExpr
	ast.Inspect(expr, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if name, _ := mergePatchConstructorName(pass, call); name != "" {
			calls = append(calls, call)
		}
		return true
	})
	return calls
}

func findMergeAssignments(pass *analysis.Pass, ident *ast.Ident, body *ast.BlockStmt, patchPos token.Pos) []*ast.CallExpr {
	obj := pass.TypesInfo.ObjectOf(ident)
	if obj == nil {
		return nil
	}
	var rhsList []ast.Expr
	collectReachingAssigns(pass, body.List, obj, patchPos, &rhsList)
	var calls []*ast.CallExpr
	for _, rhs := range rhsList {
		calls = append(calls, collectMergePatchConstructorCalls(pass, rhs)...)
	}
	return calls
}

// collectReachingAssigns records the RHS expressions that can reach patchPos.
// Unconditional blocks replace the reaching definitions from before the block;
// conditional constructs conservatively retain both the pre-branch and
// post-branch definitions. The object identity is preserved throughout so a
// nested shadow cannot affect the outer patch variable.
func collectReachingAssigns(pass *analysis.Pass, stmts []ast.Stmt, obj types.Object, patchPos token.Pos, out *[]ast.Expr) {
	defs, found := reachingDefinitionsInList(pass, stmts, obj, patchPos, nil)
	if found {
		*out = appendUniqueExpressions(*out, defs...)
	}
}

func rhsAssignedToObject(pass *analysis.Pass, stmt ast.Stmt, obj types.Object) ast.Expr {
	switch stmt := stmt.(type) {
	case *ast.AssignStmt:
		return rhsForObject(pass, stmt, obj)
	case *ast.DeclStmt:
		gen, ok := stmt.Decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.VAR {
			return nil
		}
		for _, spec := range gen.Specs {
			valueSpec, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range valueSpec.Names {
				if pass.TypesInfo.ObjectOf(name) != obj {
					continue
				}
				if len(valueSpec.Values) == 1 {
					return valueSpec.Values[0]
				}
				if i < len(valueSpec.Values) {
					return valueSpec.Values[i]
				}
			}
		}
	}
	return nil
}

func rhsForObject(pass *analysis.Pass, assign *ast.AssignStmt, obj types.Object) ast.Expr {
	if len(assign.Rhs) == 0 {
		return nil
	}
	for i, lhs := range assign.Lhs {
		id, ok := lhs.(*ast.Ident)
		if !ok || pass.TypesInfo.ObjectOf(id) != obj {
			continue
		}
		if len(assign.Rhs) == 1 {
			return assign.Rhs[0]
		}
		if i < len(assign.Rhs) {
			return assign.Rhs[i]
		}
	}
	return nil
}

func reachingDefinitionsInList(pass *analysis.Pass, stmts []ast.Stmt, obj types.Object, patchPos token.Pos, defs []ast.Expr) ([]ast.Expr, bool) {
	for _, stmt := range stmts {
		if patchPos < stmt.Pos() {
			break
		}
		if containsPosition(stmt, patchPos) {
			return reachingDefinitionsInNode(pass, stmt, obj, patchPos, defs), true
		}
		defs = advanceDefinitionsForStmt(pass, stmt, obj, defs)
	}
	return defs, false
}

// reachingDefinitionsInNode follows the lexical control-flow container that
// contains patchPos. Statements outside that container have already been
// processed by reachingDefinitionsInList.
func reachingDefinitionsInNode(pass *analysis.Pass, node ast.Node, obj types.Object, patchPos token.Pos, defs []ast.Expr) []ast.Expr {
	switch node := node.(type) {
	case *ast.BlockStmt:
		return reachingDefinitionsInBlock(pass, node, obj, patchPos, defs)
	case *ast.IfStmt:
		return reachingDefinitionsInIf(pass, node, obj, patchPos, defs)
	case *ast.ForStmt:
		return reachingDefinitionsInFor(pass, node, obj, patchPos, defs)
	case *ast.RangeStmt:
		return reachingDefinitionsInRange(pass, node, obj, patchPos, defs)
	case *ast.SwitchStmt:
		return reachingDefinitionsInSwitch(pass, node, obj, patchPos, defs)
	case *ast.TypeSwitchStmt:
		return reachingDefinitionsInTypeSwitch(pass, node, obj, patchPos, defs)
	case *ast.SelectStmt:
		return reachingDefinitionsInSelect(pass, node, obj, patchPos, defs)
	case *ast.FuncLit:
		return reachingDefinitionsInFuncLit(pass, node, obj, patchPos, defs)
	case *ast.CaseClause:
		return reachingDefinitionsInCaseClause(pass, node, obj, patchPos, defs)
	case *ast.CommClause:
		return reachingDefinitionsInCommClause(pass, node, obj, patchPos, defs)
	case *ast.LabeledStmt:
		return reachingDefinitionsInLabeledStmt(pass, node, obj, patchPos, defs)
	}

	return reachingDefinitionsInNestedNode(pass, node, obj, patchPos, defs)
}

func reachingDefinitionsInBlock(pass *analysis.Pass, node *ast.BlockStmt, obj types.Object, patchPos token.Pos, defs []ast.Expr) []ast.Expr {
	if reaching, found := reachingDefinitionsInList(pass, node.List, obj, patchPos, defs); found {
		return reaching
	}
	return defs
}

func reachingDefinitionsInIf(pass *analysis.Pass, node *ast.IfStmt, obj types.Object, patchPos token.Pos, defs []ast.Expr) []ast.Expr {
	initDefs := defs
	if node.Init != nil {
		if containsPosition(node.Init, patchPos) {
			return reachingDefinitionsInNode(pass, node.Init, obj, patchPos, defs)
		}
		initDefs = advanceDefinitionsForStmt(pass, node.Init, obj, initDefs)
	}
	if node.Cond != nil && containsPosition(node.Cond, patchPos) {
		return reachingDefinitionsInNode(pass, node.Cond, obj, patchPos, initDefs)
	}
	if containsPosition(node.Body, patchPos) {
		return reachingDefinitionsInNode(pass, node.Body, obj, patchPos, initDefs)
	}
	if node.Else != nil && containsPosition(node.Else, patchPos) {
		return reachingDefinitionsInNode(pass, node.Else, obj, patchPos, initDefs)
	}
	return initDefs
}

func reachingDefinitionsInFor(pass *analysis.Pass, node *ast.ForStmt, obj types.Object, patchPos token.Pos, defs []ast.Expr) []ast.Expr {
	initDefs := defs
	if node.Init != nil {
		if containsPosition(node.Init, patchPos) {
			return reachingDefinitionsInNode(pass, node.Init, obj, patchPos, defs)
		}
		initDefs = advanceDefinitionsForStmt(pass, node.Init, obj, initDefs)
	}
	if node.Cond != nil && containsPosition(node.Cond, patchPos) {
		// The condition may be evaluated for the first iteration or after a
		// previous body/post iteration, so retain both possible definitions.
		iterDefs := advanceDefinitionsForStmt(pass, node.Body, obj, initDefs)
		if node.Post != nil {
			iterDefs = advanceDefinitionsForStmt(pass, node.Post, obj, iterDefs)
		}
		return appendUniqueExpressions(initDefs, iterDefs...)
	}
	if containsPosition(node.Body, patchPos) {
		return reachingDefinitionsInNode(pass, node.Body, obj, patchPos, initDefs)
	}
	if node.Post != nil && containsPosition(node.Post, patchPos) {
		bodyDefs := advanceDefinitionsForStmt(pass, node.Body, obj, initDefs)
		return reachingDefinitionsInNode(pass, node.Post, obj, patchPos, bodyDefs)
	}
	return initDefs
}

func reachingDefinitionsInRange(pass *analysis.Pass, node *ast.RangeStmt, obj types.Object, patchPos token.Pos, defs []ast.Expr) []ast.Expr {
	if containsPosition(node.X, patchPos) || containsPosition(node.Key, patchPos) || containsPosition(node.Value, patchPos) {
		return defs
	}
	if containsPosition(node.Body, patchPos) {
		return reachingDefinitionsInNode(pass, node.Body, obj, patchPos, defs)
	}
	return defs
}

func reachingDefinitionsInSwitch(pass *analysis.Pass, node *ast.SwitchStmt, obj types.Object, patchPos token.Pos, defs []ast.Expr) []ast.Expr {
	initDefs := defs
	if node.Init != nil {
		if containsPosition(node.Init, patchPos) {
			return reachingDefinitionsInNode(pass, node.Init, obj, patchPos, defs)
		}
		initDefs = advanceDefinitionsForStmt(pass, node.Init, obj, initDefs)
	}
	if node.Tag != nil && containsPosition(node.Tag, patchPos) {
		return reachingDefinitionsInNode(pass, node.Tag, obj, patchPos, initDefs)
	}
	if containsPosition(node.Body, patchPos) {
		return reachingDefinitionsInNode(pass, node.Body, obj, patchPos, initDefs)
	}
	return initDefs
}

func reachingDefinitionsInTypeSwitch(pass *analysis.Pass, node *ast.TypeSwitchStmt, obj types.Object, patchPos token.Pos, defs []ast.Expr) []ast.Expr {
	initDefs := defs
	if node.Init != nil {
		if containsPosition(node.Init, patchPos) {
			return reachingDefinitionsInNode(pass, node.Init, obj, patchPos, defs)
		}
		initDefs = advanceDefinitionsForStmt(pass, node.Init, obj, initDefs)
	}
	if node.Assign != nil && containsPosition(node.Assign, patchPos) {
		return reachingDefinitionsInNode(pass, node.Assign, obj, patchPos, initDefs)
	}
	if containsPosition(node.Body, patchPos) {
		return reachingDefinitionsInNode(pass, node.Body, obj, patchPos, initDefs)
	}
	return initDefs
}

func reachingDefinitionsInSelect(pass *analysis.Pass, node *ast.SelectStmt, obj types.Object, patchPos token.Pos, defs []ast.Expr) []ast.Expr {
	if containsPosition(node.Body, patchPos) {
		return reachingDefinitionsInNode(pass, node.Body, obj, patchPos, defs)
	}
	return defs
}

func reachingDefinitionsInFuncLit(pass *analysis.Pass, node *ast.FuncLit, obj types.Object, patchPos token.Pos, defs []ast.Expr) []ast.Expr {
	if node.Body != nil && containsPosition(node.Body, patchPos) {
		return reachingDefinitionsInBlock(pass, node.Body, obj, patchPos, defs)
	}
	return defs
}

func reachingDefinitionsInCaseClause(pass *analysis.Pass, node *ast.CaseClause, obj types.Object, patchPos token.Pos, defs []ast.Expr) []ast.Expr {
	if containsPositionInStmts(node.Body, patchPos) {
		return reachingDefinitionsInListResult(pass, node.Body, obj, patchPos, defs)
	}
	return defs
}

func reachingDefinitionsInCommClause(pass *analysis.Pass, node *ast.CommClause, obj types.Object, patchPos token.Pos, defs []ast.Expr) []ast.Expr {
	if containsPositionInStmts(node.Body, patchPos) {
		return reachingDefinitionsInListResult(pass, node.Body, obj, patchPos, defs)
	}
	return defs
}

func reachingDefinitionsInListResult(pass *analysis.Pass, stmts []ast.Stmt, obj types.Object, patchPos token.Pos, defs []ast.Expr) []ast.Expr {
	if reaching, found := reachingDefinitionsInList(pass, stmts, obj, patchPos, defs); found {
		return reaching
	}
	return defs
}

func reachingDefinitionsInLabeledStmt(pass *analysis.Pass, node *ast.LabeledStmt, obj types.Object, patchPos token.Pos, defs []ast.Expr) []ast.Expr {
	if containsPosition(node.Stmt, patchPos) {
		return reachingDefinitionsInNode(pass, node.Stmt, obj, patchPos, defs)
	}
	return defs
}

func reachingDefinitionsInNestedNode(pass *analysis.Pass, node ast.Node, obj types.Object, patchPos token.Pos, defs []ast.Expr) []ast.Expr {
	// The target is often nested in a return or expression statement. Descend
	// only through the child whose source range contains patchPos; the control
	// flow cases above handle the nodes that can change reaching definitions.
	var child ast.Node
	ast.Inspect(node, func(candidate ast.Node) bool {
		if candidate == nil || candidate == node {
			return candidate == node
		}
		if containsPosition(candidate, patchPos) {
			child = candidate
		}
		return false
	})
	if child != nil {
		return reachingDefinitionsInNode(pass, child, obj, patchPos, defs)
	}
	return defs
}

func advanceDefinitionsForStmt(pass *analysis.Pass, stmt ast.Stmt, obj types.Object, defs []ast.Expr) []ast.Expr {
	switch stmt := stmt.(type) {
	case *ast.BlockStmt:
		return advanceDefinitionsForList(pass, stmt.List, obj, defs)
	case *ast.IfStmt:
		initDefs := defs
		if stmt.Init != nil {
			initDefs = advanceDefinitionsForStmt(pass, stmt.Init, obj, initDefs)
		}
		paths := [][]ast.Expr{initDefs, advanceDefinitionsForStmt(pass, stmt.Body, obj, initDefs)}
		if stmt.Else != nil {
			paths = append(paths, advanceDefinitionsForStmt(pass, stmt.Else, obj, initDefs))
		}
		return unionDefinitions(paths...)
	case *ast.ForStmt:
		initDefs := defs
		if stmt.Init != nil {
			initDefs = advanceDefinitionsForStmt(pass, stmt.Init, obj, initDefs)
		}
		bodyDefs := advanceDefinitionsForStmt(pass, stmt.Body, obj, initDefs)
		paths := [][]ast.Expr{initDefs, bodyDefs}
		if stmt.Post != nil {
			paths = append(paths, advanceDefinitionsForStmt(pass, stmt.Post, obj, bodyDefs))
		}
		return unionDefinitions(paths...)
	case *ast.RangeStmt:
		return unionDefinitions(defs, advanceDefinitionsForStmt(pass, stmt.Body, obj, defs))
	case *ast.SwitchStmt:
		initDefs := defs
		if stmt.Init != nil {
			initDefs = advanceDefinitionsForStmt(pass, stmt.Init, obj, initDefs)
		}
		return advanceDefinitionsForSwitchBody(pass, stmt.Body, obj, initDefs)
	case *ast.TypeSwitchStmt:
		initDefs := defs
		if stmt.Init != nil {
			initDefs = advanceDefinitionsForStmt(pass, stmt.Init, obj, initDefs)
		}
		return advanceDefinitionsForSwitchBody(pass, stmt.Body, obj, initDefs)
	case *ast.SelectStmt:
		return advanceDefinitionsForSwitchBody(pass, stmt.Body, obj, defs)
	case *ast.CaseClause:
		return advanceDefinitionsForList(pass, stmt.Body, obj, defs)
	case *ast.CommClause:
		return advanceDefinitionsForList(pass, stmt.Body, obj, defs)
	case *ast.LabeledStmt:
		return advanceDefinitionsForStmt(pass, stmt.Stmt, obj, defs)
	default:
		if rhs := rhsAssignedToObject(pass, stmt, obj); rhs != nil {
			return []ast.Expr{rhs}
		}
		return defs
	}
}

func advanceDefinitionsForList(pass *analysis.Pass, stmts []ast.Stmt, obj types.Object, defs []ast.Expr) []ast.Expr {
	for _, stmt := range stmts {
		defs = advanceDefinitionsForStmt(pass, stmt, obj, defs)
	}
	return defs
}

func advanceDefinitionsForSwitchBody(pass *analysis.Pass, body *ast.BlockStmt, obj types.Object, defs []ast.Expr) []ast.Expr {
	if body == nil {
		return defs
	}
	paths := [][]ast.Expr{defs}
	for _, stmt := range body.List {
		paths = append(paths, advanceDefinitionsForStmt(pass, stmt, obj, defs))
	}
	return unionDefinitions(paths...)
}

func containsPosition(node ast.Node, pos token.Pos) bool {
	return node != nil && node.Pos() <= pos && pos < node.End()
}

func containsPositionInStmts(stmts []ast.Stmt, pos token.Pos) bool {
	for _, stmt := range stmts {
		if containsPosition(stmt, pos) {
			return true
		}
	}
	return false
}

func unionDefinitions(groups ...[]ast.Expr) []ast.Expr {
	var result []ast.Expr
	for _, group := range groups {
		result = appendUniqueExpressions(result, group...)
	}
	return result
}

func appendUniqueExpressions(expressions []ast.Expr, additions ...ast.Expr) []ast.Expr {
	for _, addition := range additions {
		if addition == nil {
			continue
		}
		duplicate := false
		for _, expression := range expressions {
			if expression.Pos() == addition.Pos() && expression.End() == addition.End() {
				duplicate = true
				break
			}
		}
		if !duplicate {
			expressions = append(expressions, addition)
		}
	}
	return expressions
}

func mergePatchConstructorName(pass *analysis.Pass, call *ast.CallExpr) (name string, isControllerRuntime bool) {
	obj := funcObject(pass, call.Fun)
	if obj == nil {
		return "", false
	}
	name = obj.Name()
	if name != "MergeFrom" && name != "MergeFromWithOptions" {
		return "", false
	}
	if isFromPackage(obj, controllerRuntimeClientPkg) {
		return name, true
	}
	return name, false
}

// funcObject resolves a call's function. Package-level functions such as
// client.MergeFrom are recorded in TypesInfo.Uses on the selector Ident, not in
// TypesInfo.Selections (which only records method selections on values).
func funcObject(pass *analysis.Pass, fun ast.Expr) *types.Func {
	switch fn := fun.(type) {
	case *ast.Ident:
		obj, _ := pass.TypesInfo.Uses[fn].(*types.Func)
		return obj
	case *ast.SelectorExpr:
		if sel, ok := pass.TypesInfo.Selections[fn]; ok {
			obj, _ := sel.Obj().(*types.Func)
			return obj
		}
		obj, _ := pass.TypesInfo.Uses[fn.Sel].(*types.Func)
		return obj
	default:
		return nil
	}
}

func hasOptimisticLockOption(pass *analysis.Pass, call *ast.CallExpr) bool {
	for _, arg := range call.Args[1:] {
		if isOptimisticLockOption(pass, arg) {
			return true
		}
	}
	return false
}

func isOptimisticLockOption(pass *analysis.Pass, expr ast.Expr) bool {
	named := namedType(pass.TypesInfo.TypeOf(expr))
	if named == nil {
		return false
	}
	obj := named.Obj()
	return obj.Name() == optimisticLockType && isFromPackage(obj, controllerRuntimeClientPkg)
}

func namedType(t types.Type) *types.Named {
	if t == nil {
		return nil
	}
	t = types.Unalias(t)
	if ptr, ok := t.(*types.Pointer); ok {
		t = types.Unalias(ptr.Elem())
	}
	named, _ := t.(*types.Named)
	return named
}

func isFromPackage(obj types.Object, path string) bool {
	return obj != nil && obj.Pkg() != nil && obj.Pkg().Path() == path
}

// isHostedControlPlane reports whether t is (a pointer to) HyperShift's
// HostedControlPlane type. Matching requires both the type name and the
// hypershift/v1beta1 package path so a local or third-party type of the same
// name does not trigger the rule.
func isHostedControlPlane(t types.Type) bool {
	named := namedType(t)
	if named == nil {
		return false
	}
	obj := named.Obj()
	return obj.Name() == hostedControlPlaneType && isFromPackage(obj, hostedControlPlanePkg)
}
