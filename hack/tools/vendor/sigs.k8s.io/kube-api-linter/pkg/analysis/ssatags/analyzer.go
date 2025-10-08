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
package ssatags

import (
	"fmt"
	"go/ast"

	"golang.org/x/tools/go/analysis"

	kalerrors "sigs.k8s.io/kube-api-linter/pkg/analysis/errors"
	"sigs.k8s.io/kube-api-linter/pkg/analysis/helpers/extractjsontags"
	"sigs.k8s.io/kube-api-linter/pkg/analysis/helpers/inspector"
	"sigs.k8s.io/kube-api-linter/pkg/analysis/helpers/markers"
	"sigs.k8s.io/kube-api-linter/pkg/analysis/utils"
	kubebuildermarkers "sigs.k8s.io/kube-api-linter/pkg/markers"
)

const name = "ssatags"

const (
	listTypeAtomic = "atomic"
	listTypeSet    = "set"
	listTypeMap    = "map"
)

type analyzer struct {
	listTypeSetUsage SSATagsListTypeSetUsage
}

func newAnalyzer(cfg *SSATagsConfig) *analysis.Analyzer {
	if cfg == nil {
		cfg = &SSATagsConfig{}
	}

	defaultConfig(cfg)

	a := &analyzer{
		listTypeSetUsage: cfg.ListTypeSetUsage,
	}

	return &analysis.Analyzer{
		Name:     name,
		Doc:      "Check that all array types in the API have a listType tag and the usage of the tags is correct",
		Run:      a.run,
		Requires: []*analysis.Analyzer{inspector.Analyzer, extractjsontags.Analyzer},
	}
}

func (a *analyzer) run(pass *analysis.Pass) (any, error) {
	inspect, ok := pass.ResultOf[inspector.Analyzer].(inspector.Inspector)
	if !ok {
		return nil, kalerrors.ErrCouldNotGetInspector
	}

	inspect.InspectFields(func(field *ast.Field, stack []ast.Node, jsonTagInfo extractjsontags.FieldTagInfo, markersAccess markers.Markers) {
		a.checkField(pass, field, markersAccess)
	})

	return nil, nil //nolint:nilnil
}

func (a *analyzer) checkField(pass *analysis.Pass, field *ast.Field, markersAccess markers.Markers) {
	if !utils.IsArrayTypeOrAlias(pass, field) {
		return
	}

	fieldMarkers := utils.TypeAwareMarkerCollectionForField(pass, markersAccess, field)
	if fieldMarkers == nil {
		return
	}

	// If the field is a byte array, we cannot use listType markers with it.
	if utils.IsByteArray(pass, field) {
		listTypeMarkers := fieldMarkers.Get(kubebuildermarkers.KubebuilderListTypeMarker)
		for _, marker := range listTypeMarkers {
			pass.Report(analysis.Diagnostic{
				Pos:     field.Pos(),
				Message: fmt.Sprintf("%s is a byte array, which does not support the listType marker. Remove the listType marker", utils.FieldName(field)),
				SuggestedFixes: []analysis.SuggestedFix{
					{
						Message: fmt.Sprintf("Remove listType marker from %s", utils.FieldName(field)),
						TextEdits: []analysis.TextEdit{
							{
								Pos:     marker.Pos,
								End:     marker.End + 1,
								NewText: []byte(""),
							},
						},
					},
				},
			})
		}

		return
	}

	fieldName := utils.FieldName(field)
	listTypeMarkers := fieldMarkers.Get(kubebuildermarkers.KubebuilderListTypeMarker)

	if len(listTypeMarkers) == 0 {
		pass.Report(analysis.Diagnostic{
			Pos:     field.Pos(),
			Message: fmt.Sprintf("%s should have a listType marker for proper Server-Side Apply behavior (atomic, set, or map)", fieldName),
		})

		return
	}

	for _, marker := range listTypeMarkers {
		listType := marker.Expressions[""]

		a.checkListTypeMarker(pass, listType, field)

		if listType == listTypeMap {
			a.checkListTypeMap(pass, fieldMarkers, field)
		}

		if listType == listTypeSet {
			a.checkListTypeSet(pass, field)
		}
	}
}

func (a *analyzer) checkListTypeMarker(pass *analysis.Pass, listType string, field *ast.Field) {
	fieldName := utils.FieldName(field)

	if !validListType(listType) {
		pass.Report(analysis.Diagnostic{
			Pos:     field.Pos(),
			Message: fmt.Sprintf("%s has invalid listType %q, must be one of: atomic, set, map", fieldName, listType),
		})

		return
	}
}

func (a *analyzer) checkListTypeMap(pass *analysis.Pass, fieldMarkers markers.MarkerSet, field *ast.Field) {
	listMapKeyMarkers := fieldMarkers.Get(kubebuildermarkers.KubebuilderListMapKeyMarker)
	fieldName := utils.FieldName(field)

	isObjectList := utils.IsObjectList(pass, field)

	if !isObjectList {
		pass.Report(analysis.Diagnostic{
			Pos:     field.Pos(),
			Message: fmt.Sprintf("%s with listType=map can only be used for object lists, not primitive lists", fieldName),
		})

		return
	}

	if len(listMapKeyMarkers) == 0 {
		pass.Report(analysis.Diagnostic{
			Pos:     field.Pos(),
			Message: fmt.Sprintf("%s with listType=map must have at least one listMapKey marker", fieldName),
		})

		return
	}

	a.validateListMapKeys(pass, field, listMapKeyMarkers)
}

func (a *analyzer) checkListTypeSet(pass *analysis.Pass, field *ast.Field) {
	if a.listTypeSetUsage == SSATagsListTypeSetUsageIgnore {
		return
	}

	isObjectList := utils.IsObjectList(pass, field)
	if !isObjectList {
		return
	}

	fieldName := utils.FieldName(field)
	diagnostic := analysis.Diagnostic{
		Pos:     field.Pos(),
		Message: fmt.Sprintf("%s with listType=set is not recommended due to Server-Side Apply compatibility issues. Consider using listType=%s or listType=%s instead", fieldName, listTypeAtomic, listTypeMap),
	}

	pass.Report(diagnostic)
}

func (a *analyzer) validateListMapKeys(pass *analysis.Pass, field *ast.Field, listMapKeyMarkers []markers.Marker) {
	jsonTags, ok := pass.ResultOf[extractjsontags.Analyzer].(extractjsontags.StructFieldTags)
	if !ok {
		return
	}

	structFields := a.getStructFieldsFromField(pass, field)
	if structFields == nil {
		return
	}

	fieldName := utils.FieldName(field)

	for _, marker := range listMapKeyMarkers {
		keyName := marker.Expressions[""]
		if keyName == "" {
			continue
		}

		if !a.hasFieldWithJSONTag(structFields, jsonTags, keyName) {
			pass.Report(analysis.Diagnostic{
				Pos:     field.Pos(),
				Message: fmt.Sprintf("%s listMapKey %q does not exist as a field in the struct", fieldName, keyName),
			})
		}
	}
}

func (a *analyzer) getStructFieldsFromField(pass *analysis.Pass, field *ast.Field) *ast.FieldList {
	var elementType ast.Expr

	// Get the element type from array or field type
	if arrayType, ok := field.Type.(*ast.ArrayType); ok {
		elementType = arrayType.Elt
	} else {
		elementType = field.Type
	}

	return a.getStructFieldsFromExpr(pass, elementType)
}

func (a *analyzer) getStructFieldsFromExpr(pass *analysis.Pass, expr ast.Expr) *ast.FieldList {
	switch elementType := expr.(type) {
	case *ast.Ident:
		typeSpec, ok := utils.LookupTypeSpec(pass, elementType)
		if !ok {
			return nil
		}

		structType, ok := typeSpec.Type.(*ast.StructType)
		if !ok {
			return nil
		}

		return structType.Fields
	case *ast.StarExpr:
		return a.getStructFieldsFromExpr(pass, elementType.X)
	case *ast.SelectorExpr:
		return nil
	}

	return nil
}

func (a *analyzer) hasFieldWithJSONTag(fields *ast.FieldList, jsonTags extractjsontags.StructFieldTags, fieldName string) bool {
	if fields == nil {
		return false
	}

	for _, field := range fields.List {
		tagInfo := jsonTags.FieldTags(field)

		if tagInfo.Name == fieldName {
			return true
		}
	}

	return false
}

func validListType(listType string) bool {
	switch listType {
	case listTypeAtomic, listTypeSet, listTypeMap:
		return true
	default:
		return false
	}
}

func defaultConfig(cfg *SSATagsConfig) {
	if cfg.ListTypeSetUsage == "" {
		cfg.ListTypeSetUsage = SSATagsListTypeSetUsageWarn
	}
}
