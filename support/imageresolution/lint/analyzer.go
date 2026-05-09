package lint

import (
	"go/ast"
	"go/token"
	"slices"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

// Analyzer is a go/analysis pass that flags bypass of the imageresolution abstraction boundary.
// It checks for raw override map parameters, banned registry client imports, and references to
// deprecated decorator types.
var Analyzer = &analysis.Analyzer{
	Name:     "imageresolution",
	Doc:      "flags bypass of the imageresolution abstraction boundary",
	Run:      run,
	Requires: []*analysis.Analyzer{inspect.Analyzer},
}

var overrideParamNames = []string{
	"registryoverrides",
	"imageregistryoverrides",
	"imageregistrymirrors",
	"openshiftimageregistryoverrides",
	"openshift_img_overrides",
}

var bannedImportPrefixes = []string{
	"github.com/google/go-containerregistry",
	"github.com/containers/image",
}

var bannedTypeNames = []string{
	"ProviderWithRegistryOverrides",
	"ProviderWithOpenShiftImageRegistryOverrides",
	"RegistryMirrorProviderDecorator",
	"ProviderWithOpenShiftImageRegistryOverridesDecorator",
	"CommonRegistryProvider",
}

func run(pass *analysis.Pass) (any, error) {
	if isExempt(pass.Pkg.Path()) || pass.Pkg.Name() == "main" {
		return nil, nil
	}

	checkImports(pass)
	checkParams(pass)
	checkStructFields(pass)
	checkTypeReferences(pass)
	return nil, nil
}

func checkImports(pass *analysis.Pass) {
	for _, file := range pass.Files {
		for _, imp := range file.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if isBannedImport(path) {
				pass.Reportf(imp.Pos(), "direct import of registry/release package %q: use imageresolution package instead", path)
			}
		}
	}
}

func checkParams(pass *analysis.Pass) {
	insp := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	insp.Preorder([]ast.Node{(*ast.FuncDecl)(nil)}, func(n ast.Node) {
		fn := n.(*ast.FuncDecl)
		if isTestFile(pass, fn.Pos()) || fn.Type.Params == nil {
			return
		}
		for _, field := range fn.Type.Params.List {
			if isRawOverrideMap(pass, field) {
				for _, name := range field.Names {
					pass.Reportf(name.Pos(), "raw override map parameter %q: use imageresolution.ResolverConfig instead", name.Name)
				}
			}
		}
	})
}

func checkStructFields(pass *analysis.Pass) {
	insp := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	insp.Preorder([]ast.Node{(*ast.StructType)(nil)}, func(n ast.Node) {
		st := n.(*ast.StructType)
		if isTestFile(pass, st.Pos()) || st.Fields == nil {
			return
		}
		for _, field := range st.Fields.List {
			if isRawOverrideMap(pass, field) {
				for _, name := range field.Names {
					pass.Reportf(name.Pos(), "raw override map field %q: use imageresolution.ResolverConfig instead", name.Name)
				}
			}
		}
	})
}

func checkTypeReferences(pass *analysis.Pass) {
	insp := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	insp.Preorder([]ast.Node{(*ast.SelectorExpr)(nil)}, func(n ast.Node) {
		sel := n.(*ast.SelectorExpr)
		if isBannedTypeName(sel.Sel.Name) {
			pass.Reportf(sel.Sel.Pos(), "reference to deprecated type %q: use imageresolution.ProviderSet instead", sel.Sel.Name)
		}
	})
}

func isRawOverrideMap(pass *analysis.Pass, field *ast.Field) bool {
	t := pass.TypesInfo.TypeOf(field.Type)
	if t == nil {
		return false
	}

	isStringMap := t.String() == "map[string]string"
	isStringSliceMap := t.String() == "map[string][]string"
	if !isStringMap && !isStringSliceMap {
		return false
	}

	for _, name := range field.Names {
		if isOverrideParamName(name.Name) {
			return true
		}
	}
	return false
}

func isOverrideParamName(name string) bool {
	lower := strings.ToLower(name)
	for _, pattern := range overrideParamNames {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	return false
}

func isBannedImport(path string) bool {
	for _, prefix := range bannedImportPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func isBannedTypeName(name string) bool {
	return slices.Contains(bannedTypeNames, name)
}

var exemptPackages = []string{
	"imageresolution",
	"support/util",
	"ignition-server/cmd",
}

// exemptSuffixes matches packages by exact suffix to avoid over-broad exemptions.
var exemptSuffixes = []string{
	"hostedclusterconfigoperator",
}

func isTestFile(pass *analysis.Pass, pos token.Pos) bool {
	return strings.HasSuffix(pass.Fset.File(pos).Name(), "_test.go")
}

func isExempt(pkgPath string) bool {
	for _, exempt := range exemptPackages {
		if strings.Contains(pkgPath, exempt) {
			return true
		}
	}
	for _, suffix := range exemptSuffixes {
		if strings.HasSuffix(pkgPath, suffix) {
			return true
		}
	}
	return false
}
