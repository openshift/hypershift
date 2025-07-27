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
package optionalfields

import (
	"fmt"
	"go/ast"
	"go/token"

	"golang.org/x/tools/go/analysis"
	"sigs.k8s.io/kube-api-linter/pkg/analysis/helpers/extractjsontags"
	markershelper "sigs.k8s.io/kube-api-linter/pkg/analysis/helpers/markers"
	"sigs.k8s.io/kube-api-linter/pkg/analysis/utils"
	"sigs.k8s.io/kube-api-linter/pkg/markers"
)

// isStarExpr checks if the expression is a pointer type.
// If it is, it returns the expression inside the pointer.
func isStarExpr(expr ast.Expr) (bool, ast.Expr) {
	if ptrType, ok := expr.(*ast.StarExpr); ok {
		return true, ptrType.X
	}

	return false, expr
}

// isPointerType checks if the expression is a pointer type.
// This is for types that are always implemented as pointers and therefore should
// not be the underlying type of a star expr.
func isPointerType(pass *analysis.Pass, expr ast.Expr) bool {
	switch t := expr.(type) {
	case *ast.StarExpr, *ast.MapType, *ast.ArrayType:
		return true
	case *ast.Ident:
		// If the ident is a type alias, keep checking until we find the underlying type.
		typeSpec, ok := utils.LookupTypeSpec(pass, t)
		if !ok {
			return false
		}

		return isPointerType(pass, typeSpec.Type)
	default:
		return false
	}
}

// isFieldOptional checks if a field has an optional marker.
func isFieldOptional(fieldMarkers markershelper.MarkerSet) bool {
	return fieldMarkers.Has(markers.OptionalMarker) || fieldMarkers.Has(markers.KubebuilderOptionalMarker)
}

// reportShouldAddPointer adds an analysis diagnostic that explains that a pointer should be added.
// Where the pointer policy is suggest fix, it also adds a suggested fix to add the pointer.
func reportShouldAddPointer(pass *analysis.Pass, field *ast.Field, pointerPolicy OptionalFieldsPointerPolicy, fieldName, messageFmt string) {
	switch pointerPolicy {
	case OptionalFieldsPointerPolicySuggestFix:
		pass.Report(analysis.Diagnostic{
			Pos:     field.Pos(),
			Message: fmt.Sprintf(messageFmt, fieldName),
			SuggestedFixes: []analysis.SuggestedFix{
				{
					Message: "should make the field a pointer",
					TextEdits: []analysis.TextEdit{
						{
							Pos:     field.Pos() + token.Pos(len(fieldName)+1),
							NewText: []byte("*"),
						},
					},
				},
			},
		})
	case OptionalFieldsPointerPolicyWarn:
		pass.Reportf(field.Pos(), messageFmt, fieldName)
	default:
		panic(fmt.Sprintf("unknown pointer policy: %s", pointerPolicy))
	}
}

// reportShouldRemovePointer adds an analysis diagnostic that explains that a pointer should be removed.
// Where the pointer policy is suggest fix, it also adds a suggested fix to remove the pointer.
func reportShouldRemovePointer(pass *analysis.Pass, field *ast.Field, pointerPolicy OptionalFieldsPointerPolicy, fieldName, messageFmt string) {
	switch pointerPolicy {
	case OptionalFieldsPointerPolicySuggestFix:
		pass.Report(analysis.Diagnostic{
			Pos:     field.Pos(),
			Message: fmt.Sprintf(messageFmt, fieldName),
			SuggestedFixes: []analysis.SuggestedFix{
				{
					Message: "should remove the pointer",
					TextEdits: []analysis.TextEdit{
						{
							Pos: field.Pos() + token.Pos(len(fieldName)+1),
							End: field.Pos() + token.Pos(len(fieldName)+2),
						},
					},
				},
			},
		})
	case OptionalFieldsPointerPolicyWarn:
		pass.Reportf(field.Pos(), messageFmt, fieldName)
	default:
		panic(fmt.Sprintf("unknown pointer policy: %s", pointerPolicy))
	}
}

// reportShouldAddOmitEmpty adds an analysis diagnostic that explains that an omitempty tag should be added.
func reportShouldAddOmitEmpty(pass *analysis.Pass, field *ast.Field, omitEmptyPolicy OptionalFieldsOmitEmptyPolicy, fieldName, messageFmt string, fieldTagInfo extractjsontags.FieldTagInfo) {
	switch omitEmptyPolicy {
	case OptionalFieldsOmitEmptyPolicySuggestFix:
		pass.Report(analysis.Diagnostic{
			Pos:     field.Pos(),
			Message: fmt.Sprintf(messageFmt, fieldName),
			SuggestedFixes: []analysis.SuggestedFix{
				{
					Message: fmt.Sprintf("should add 'omitempty' to the field tag for field %s", fieldName),
					TextEdits: []analysis.TextEdit{
						{
							Pos:     fieldTagInfo.Pos + token.Pos(len(fieldTagInfo.Name)),
							NewText: []byte(",omitempty"),
						},
					},
				},
			},
		})
	case OptionalFieldsOmitEmptyPolicyWarn:
		pass.Reportf(field.Pos(), messageFmt, fieldName)
	case OptionalFieldsOmitEmptyPolicyIgnore:
		// Do nothing, as the policy is to ignore the missing omitempty tag.
	default:
		panic(fmt.Sprintf("unknown omit empty policy: %s", omitEmptyPolicy))
	}
}
