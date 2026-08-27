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
	for _, call := range mergeCallsForPatchExpr(pass, statusPatch.Args[2], enclosingFn) {
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

func mergeCallsForPatchExpr(pass *analysis.Pass, patchExpr ast.Expr, enclosingFn *ast.FuncDecl) []*ast.CallExpr {
	switch expr := patchExpr.(type) {
	case *ast.CallExpr:
		return collectMergePatchConstructorCalls(pass, expr)
	case *ast.Ident:
		if enclosingFn == nil || enclosingFn.Body == nil {
			return nil
		}
		return findMergeAssignments(pass, expr.Name, enclosingFn.Body)
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

func findMergeAssignments(pass *analysis.Pass, ident string, body *ast.BlockStmt) []*ast.CallExpr {
	var calls []*ast.CallExpr
	ast.Inspect(body, func(n ast.Node) bool {
		switch stmt := n.(type) {
		case *ast.AssignStmt:
			if !assignDefinesIdent(stmt, ident) {
				return true
			}
			for _, rhs := range stmt.Rhs {
				calls = append(calls, collectMergePatchConstructorCalls(pass, rhs)...)
			}
		}
		return true
	})
	return calls
}

func assignDefinesIdent(stmt *ast.AssignStmt, ident string) bool {
	for _, lhs := range stmt.Lhs {
		id, ok := lhs.(*ast.Ident)
		if ok && id.Name == ident {
			return true
		}
	}
	return false
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
