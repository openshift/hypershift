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
package inspector

import (
	"go/ast"
	"go/token"

	astinspector "golang.org/x/tools/go/ast/inspector"
	"sigs.k8s.io/kube-api-linter/pkg/analysis/helpers/extractjsontags"
	"sigs.k8s.io/kube-api-linter/pkg/analysis/helpers/markers"
)

// Inspector is an interface that allows for the inspection of fields in structs.
type Inspector interface {
	// InspectFields is a function that iterates over fields in structs.
	InspectFields(func(field *ast.Field, stack []ast.Node, jsonTagInfo extractjsontags.FieldTagInfo, markersAccess markers.Markers))

	// InspectTypeSpec is a function that inspects the type spec and calls the provided inspectTypeSpec function.
	InspectTypeSpec(func(typeSpec *ast.TypeSpec, markersAccess markers.Markers))
}

// inspector implements the Inspector interface.
type inspector struct {
	inspector *astinspector.Inspector
	jsonTags  extractjsontags.StructFieldTags
	markers   markers.Markers
}

// newInspector creates a new inspector.
func newInspector(astinspector *astinspector.Inspector, jsonTags extractjsontags.StructFieldTags, markers markers.Markers) Inspector {
	return &inspector{
		inspector: astinspector,
		jsonTags:  jsonTags,
		markers:   markers,
	}
}

// InspectFields iterates over fields in structs, ignoring any struct that is not a type declaration, and any field that is ignored and
// therefore would not be included in the CRD spec.
// For the remaining fields, it calls the provided inspectField function to apply analysis logic.
func (i *inspector) InspectFields(inspectField func(field *ast.Field, stack []ast.Node, jsonTagInfo extractjsontags.FieldTagInfo, markersAccess markers.Markers)) {
	// Filter to fields so that we can iterate over fields in a struct.
	nodeFilter := []ast.Node{
		(*ast.Field)(nil),
	}

	i.inspector.WithStack(nodeFilter, func(n ast.Node, push bool, stack []ast.Node) (proceed bool) {
		if !push {
			return false
		}

		if len(stack) < 3 {
			return true
		}

		// The 0th node in the stack is the *ast.File.
		// The 1st node in the stack is the *ast.GenDecl.
		decl, ok := stack[1].(*ast.GenDecl)
		if !ok {
			// Make sure that we don't inspect structs within a function.
			return false
		}

		if decl.Tok != token.TYPE {
			// Returning false here means we won't inspect non-type declarations (e.g. var, const, import).
			return false
		}

		structType, ok := stack[len(stack)-3].(*ast.StructType)
		if !ok {
			// A field within a struct has a FieldList parent and then a StructType parent.
			// If we don't have a StructType parent, then we're not in a struct.
			return false
		}

		if isItemsType(structType) {
			// The field belongs to an items type, we don't need to report lint errors for this.
			return false
		}

		field, ok := n.(*ast.Field)
		if !ok {
			return true
		}

		tagInfo := i.jsonTags.FieldTags(field)
		if tagInfo.Ignored {
			// Returning false here means we won't inspect the children of an ignored field.
			return false
		}

		inspectField(field, stack, tagInfo, i.markers)

		return true
	})
}

// InspectTypeSpec inspects the type spec and calls the provided inspectTypeSpec function.
func (i *inspector) InspectTypeSpec(inspectTypeSpec func(typeSpec *ast.TypeSpec, markersAccess markers.Markers)) {
	nodeFilter := []ast.Node{
		(*ast.TypeSpec)(nil),
	}

	i.inspector.Preorder(nodeFilter, func(n ast.Node) {
		typeSpec, ok := n.(*ast.TypeSpec)
		if !ok {
			return
		}

		inspectTypeSpec(typeSpec, i.markers)
	})
}

func isItemsType(structType *ast.StructType) bool {
	// An items type is a struct with TypeMeta, ListMeta and Items fields.
	if len(structType.Fields.List) != 3 {
		return false
	}

	// Check if the first field is TypeMeta.
	// This should be a selector (e.g. metav1.TypeMeta)
	// Check the TypeMeta part as the package name may vary.
	if typeMeta, ok := structType.Fields.List[0].Type.(*ast.SelectorExpr); !ok || typeMeta.Sel.Name != "TypeMeta" {
		return false
	}

	// Check if the second field is ListMeta.
	if listMeta, ok := structType.Fields.List[1].Type.(*ast.SelectorExpr); !ok || listMeta.Sel.Name != "ListMeta" {
		return false
	}

	// Check if the third field is Items.
	// It should be an array, and be called Items.
	itemsField := structType.Fields.List[2]
	if _, ok := itemsField.Type.(*ast.ArrayType); !ok || len(itemsField.Names) == 0 || itemsField.Names[0].Name != "Items" {
		return false
	}

	return true
}
