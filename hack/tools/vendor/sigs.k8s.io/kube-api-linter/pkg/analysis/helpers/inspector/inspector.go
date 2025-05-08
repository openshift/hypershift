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

		_, ok = stack[len(stack)-3].(*ast.StructType)
		if !ok {
			// A field within a struct has a FieldList parent and then a StructType parent.
			// If we don't have a StructType parent, then we're not in a struct.
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
