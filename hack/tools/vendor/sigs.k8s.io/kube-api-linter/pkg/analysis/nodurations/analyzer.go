/*
Copyright 2025 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package nodurations

import (
	"fmt"
	"go/ast"

	"golang.org/x/tools/go/analysis"
	kalerrors "sigs.k8s.io/kube-api-linter/pkg/analysis/errors"
	"sigs.k8s.io/kube-api-linter/pkg/analysis/helpers/extractjsontags"
	"sigs.k8s.io/kube-api-linter/pkg/analysis/helpers/inspector"
	"sigs.k8s.io/kube-api-linter/pkg/analysis/helpers/markers"
	"sigs.k8s.io/kube-api-linter/pkg/analysis/utils"
)

const name = "nodurations"

// Analyzer is the analyzer for the nodurations package.
// It checks that no struct field is of a type either time.Duration or metav1.Duration.
var Analyzer = &analysis.Analyzer{
	Name:     name,
	Doc:      "Duration types should not be used, to avoid the need for clients to implement go duration parsing. Instead, use integer based fields with the unit in the field name.",
	Run:      run,
	Requires: []*analysis.Analyzer{inspector.Analyzer},
}

func run(pass *analysis.Pass) (any, error) {
	inspect, ok := pass.ResultOf[inspector.Analyzer].(inspector.Inspector)
	if !ok {
		return nil, kalerrors.ErrCouldNotGetInspector
	}

	inspect.InspectFields(func(field *ast.Field, _ extractjsontags.FieldTagInfo, markersAccess markers.Markers, qualifiedFieldName string) {
		checkField(pass, field, qualifiedFieldName)
	})

	inspect.InspectTypeSpec(func(typeSpec *ast.TypeSpec, markersAccess markers.Markers) {
		checkTypeSpec(pass, typeSpec, typeSpec, "type")
	})

	return nil, nil //nolint:nilnil
}

func checkField(pass *analysis.Pass, field *ast.Field, qualifiedFieldName string) {
	prefix := fmt.Sprintf("field %s", qualifiedFieldName)

	checkTypeExpr(pass, field.Type, field, prefix)
}

//nolint:cyclop
func checkTypeExpr(pass *analysis.Pass, typeExpr ast.Expr, node ast.Node, prefix string) {
	switch typ := typeExpr.(type) {
	case *ast.SelectorExpr:
		pkg, ok := typ.X.(*ast.Ident)
		if !ok {
			return
		}

		if typ.X == nil || (pkg.Name != "time" && pkg.Name != "metav1") {
			return
		}

		// Array element is not a metav1.Condition.
		if typ.Sel == nil || typ.Sel.Name != "Duration" {
			return
		}

		pass.Reportf(node.Pos(), "%s should not use a Duration. Use an integer type with units in the name to avoid the need for clients to implement Go style duration parsing.", prefix)
	case *ast.Ident:
		checkIdent(pass, typ, node, prefix)
	case *ast.StarExpr:
		checkTypeExpr(pass, typ.X, node, fmt.Sprintf("%s pointer", prefix))
	case *ast.ArrayType:
		checkTypeExpr(pass, typ.Elt, node, fmt.Sprintf("%s array element", prefix))
	case *ast.MapType:
		checkTypeExpr(pass, typ.Key, node, fmt.Sprintf("%s map key", prefix))
		checkTypeExpr(pass, typ.Value, node, fmt.Sprintf("%s map value", prefix))
	}
}

// checkIdent calls the checkFunc with the ident, when we have hit a built-in type.
// If the ident is not a built in, we look at the underlying type until we hit a built-in type.
func checkIdent(pass *analysis.Pass, ident *ast.Ident, node ast.Node, prefix string) {
	if utils.IsBasicType(pass, ident) {
		// We've hit a built-in type, no need to check further.
		return
	}

	tSpec, ok := utils.LookupTypeSpec(pass, ident)
	if !ok {
		return
	}

	// The field is using a type alias, check if the alias is an int.
	checkTypeSpec(pass, tSpec, node, fmt.Sprintf("%s type", prefix))
}

func checkTypeSpec(pass *analysis.Pass, tSpec *ast.TypeSpec, node ast.Node, prefix string) {
	if tSpec.Name == nil {
		return
	}

	typeName := tSpec.Name.Name
	prefix = fmt.Sprintf("%s %s", prefix, typeName)

	checkTypeExpr(pass, tSpec.Type, node, prefix)
}
