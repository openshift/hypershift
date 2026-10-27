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

var Analyzer = newAnalyzer(allowlist)

func newAnalyzer(allowed map[string]map[string][]string) *analysis.Analyzer {
	return &analysis.Analyzer{
		Name: "e2eutilallowlist",
		Doc:  "denies legacy e2e utility references in v2 outside the per-file, per-scope baseline",
		Run:  func(pass *analysis.Pass) (any, error) { return run(pass, allowed) },
	}
}

func run(pass *analysis.Pass, allowed map[string]map[string][]string) (any, error) {
	for _, file := range pass.Files {
		filename := filepath.ToSlash(pass.Fset.File(file.Pos()).Name())
		if !pathutil.IsV2E2ETest(filename) {
			continue
		}
		filename = filename[strings.LastIndex(filename, "test/e2e/v2/"):]

		// Ordinary closures inherit their enclosing scope. Ginkgo callbacks get
		// distinct scopes so new specs cannot inherit a suite or hook allowance.
		members := map[*ast.Ident]string{}
		used := map[[2]string]bool{} // enclosing scope and utility symbol
		registrations := map[string]int{}
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
					if err == nil && isLegacyUtilPkg(pkgPath) {
						pass.Reportf(node.Pos(), "side-effect import of %s is not allowed from test/e2e/v2; remove the legacy utility dependency", pkgPath)
					}
				}
			case *ast.SelectorExpr:
				if selection := pass.TypesInfo.Selections[node]; selection != nil {
					receiver, indices := selection.Recv(), selection.Index()
					if method, ok := selection.Obj().(*types.Func); ok {
						if signature := method.Type().(*types.Signature); signature.Recv() != nil {
							receiver = signature.Recv().Type()
						}
						indices = nil
					}
					members[node.Sel] = memberName(receiver, indices, node.Sel.Name)
				}
			case *ast.CompositeLit:
				for _, element := range node.Elts {
					if pair, ok := element.(*ast.KeyValueExpr); ok {
						if key, ok := pair.Key.(*ast.Ident); ok {
							if field, ok := pass.TypesInfo.Uses[key].(*types.Var); ok && field.IsField() {
								members[key] = memberName(pass.TypesInfo.TypeOf(node), nil, key.Name)
							}
						}
					}
				}
			case *ast.FuncDecl:
				scope = node.Name.Name
				if node.Recv != nil {
					// Comma-ok defensively: a missing Defs entry or a receiverless
					// signature would otherwise panic the entire lint run.
					if fn, ok := pass.TypesInfo.Defs[node.Name].(*types.Func); ok {
						if signature, ok := fn.Type().(*types.Signature); ok && signature.Recv() != nil {
							recv := signature.Recv().Type()
							if ptr, ok := recv.(*types.Pointer); ok {
								recv = ptr.Elem()
							}
							scope = types.TypeString(recv, func(*types.Package) string { return "" }) + "." + scope
						}
					}
				}
			case *ast.CallExpr:
				scope = registrationScope(pass, node, scope, registrations)
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
			if !isLegacyUtilPkg(pkgPath) {
				return true
			}
			symbol := obj.Name()
			if member := members[ident]; member != "" {
				symbol = member
			}
			reference := pkgPath + "." + symbol
			if pkgPath != utilPkgPath {
				symbol = strings.TrimPrefix(pkgPath, utilPkgPath+"/") + "." + symbol
			}
			if isAllowed(allowed, filename, scope, symbol) {
				used[[2]string{scope, symbol}] = true
				return true
			}
			if scope == "" {
				scope = "package scope"
			}
			pass.Report(analysis.Diagnostic{
				Pos: ident.Pos(), End: ident.End(),
				Message: fmt.Sprintf("reference to %s is not allowed from test/e2e/v2 in %s; refactor to remove the legacy utility dependency", reference, scope),
			})
			return true
		})
		for scope, symbols := range allowed[filename] {
			for _, symbol := range symbols {
				if !used[[2]string{scope, symbol}] {
					pass.Reportf(file.Package, "stale e2eutilallowlist entry %s in %s; remove it from allowlist.go", symbol, scope)
				}
			}
		}
	}
	return nil, nil
}

// isAllowed requires an exact file, enclosing function, and utility symbol.
// There are no package-wide, file-wide, or symbol-prefix exceptions.
func isAllowed(allowed map[string]map[string][]string, filename, function, symbol string) bool {
	return slices.Contains(allowed[filename][function], symbol)
}

func isLegacyUtilPkg(pkgPath string) bool {
	return pkgPath == utilPkgPath || strings.HasPrefix(pkgPath, utilPkgPath+"/")
}

// memberName identifies the declaring type, following promoted fields through
// embedded structs. Methods pass their declared receiver rather than a promotion path.
func memberName(receiver types.Type, indices []int, name string) string {
	receiver = types.Unalias(receiver)
	if pointer, ok := receiver.(*types.Pointer); ok {
		receiver = types.Unalias(pointer.Elem())
	}
	if len(indices) > 1 {
		if structure, ok := receiver.Underlying().(*types.Struct); ok {
			return memberName(structure.Field(indices[0]).Type(), indices[1:], name)
		}
	}
	if named, ok := receiver.(*types.Named); ok {
		return named.Obj().Name() + "." + name
	}
	return types.TypeString(receiver, func(pkg *types.Package) string { return pkg.Path() }) + "." + name
}

// registrationScope isolates Ginkgo callbacks by registration kind, literal label,
// and occurrence within the parent scope. Imported aliases resolve to the same key.
func registrationScope(pass *analysis.Pass, call *ast.CallExpr, parent string, occurrences map[string]int) string {
	var identifier *ast.Ident
	switch callee := call.Fun.(type) {
	case *ast.Ident:
		identifier = callee
	case *ast.SelectorExpr:
		identifier = callee.Sel
	default:
		return parent
	}
	object := pass.TypesInfo.Uses[identifier]
	if object == nil || object.Pkg() == nil || object.Pkg().Path() != "github.com/onsi/ginkgo/v2" {
		return parent
	}
	name := object.Name()
	label := ""
	switch strings.TrimLeft(name, "FPX") {
	case "Describe", "Context", "When", "It", "Specify", "DescribeTable", "DescribeTableSubtree", "Entry":
		// Computed labels remain distinct from literal-label registrations.
		label = "<dynamic>"
		if len(call.Args) > 0 {
			if literal, ok := call.Args[0].(*ast.BasicLit); ok {
				if value, err := strconv.Unquote(literal.Value); err == nil {
					label = strconv.Quote(value)
				}
			}
		}
	case "BeforeEach", "JustBeforeEach", "BeforeAll", "BeforeSuite", "AfterEach", "JustAfterEach", "AfterAll", "AfterSuite", "SynchronizedBeforeSuite", "SynchronizedAfterSuite", "ReportBeforeSuite", "ReportAfterSuite", "ReportBeforeEach", "ReportAfterEach", "DeferCleanup":
	default:
		return parent
	}
	key := name + "(" + label + ")"
	if parent != "" {
		key = parent + "/" + key
	}
	occurrences[key]++
	return fmt.Sprintf("%s#%d", key, occurrences[key])
}
