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
package utils

import (
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis"
	"sigs.k8s.io/kube-api-linter/pkg/analysis/helpers/markers"
)

// IsBasicType checks if the type of the given identifier is a basic type.
// Basic types are types like int, string, bool, etc.
func IsBasicType(pass *analysis.Pass, ident *ast.Ident) bool {
	_, ok := pass.TypesInfo.TypeOf(ident).(*types.Basic)
	return ok
}

// IsStructType checks if the given expression is a struct type.
func IsStructType(pass *analysis.Pass, expr ast.Expr) bool {
	underlying := getUnderlyingType(expr)

	if _, ok := underlying.(*ast.StructType); ok {
		return true
	}

	// Where there's an ident, recurse to find the underlying type.
	if ident, ok := underlying.(*ast.Ident); ok {
		typeSpec, ok := LookupTypeSpec(pass, ident)
		if !ok {
			return false
		}

		return IsStructType(pass, typeSpec.Type)
	}

	return false
}

// IsStarExpr checks if the expression is a pointer type.
// If it is, it returns the expression inside the pointer.
func IsStarExpr(expr ast.Expr) (bool, ast.Expr) {
	if ptrType, ok := expr.(*ast.StarExpr); ok {
		return true, ptrType.X
	}

	return false, expr
}

// IsPointerType checks if the expression is a pointer type.
// This is for types that are always implemented as pointers and therefore should
// not be the underlying type of a star expr.
func IsPointerType(pass *analysis.Pass, expr ast.Expr) bool {
	switch t := expr.(type) {
	case *ast.StarExpr, *ast.MapType, *ast.ArrayType:
		return true
	case *ast.Ident:
		// If the ident is a type alias, keep checking until we find the underlying type.
		typeSpec, ok := LookupTypeSpec(pass, t)
		if !ok {
			return false
		}

		return IsPointerType(pass, typeSpec.Type)
	default:
		return false
	}
}

// LookupTypeSpec is used to search for the type spec of a given identifier.
// It will first check to see if the ident has an Obj, and if so, it will return the type spec
// from the Obj. If the Obj is nil, it will search through the files in the package to find the
// type spec that matches the identifier's position.
func LookupTypeSpec(pass *analysis.Pass, ident *ast.Ident) (*ast.TypeSpec, bool) {
	if ident.Obj != nil && ident.Obj.Decl != nil {
		// The identifier has an Obj, we can use it to find the type spec.
		if tSpec, ok := ident.Obj.Decl.(*ast.TypeSpec); ok {
			return tSpec, true
		}
	}

	namedType, ok := pass.TypesInfo.TypeOf(ident).(*types.Named)
	if !ok {
		return nil, false
	}

	if !isInPassPackage(pass, namedType) {
		// The identifier is not in the pass package, we can't find the type spec.
		return nil, false
	}

	tokenFile, astFile := getFilesForType(pass, ident)

	if astFile == nil {
		// We couldn't match the token.File to the ast.File.
		return nil, false
	}

	for n := range ast.Preorder(astFile) {
		tSpec, ok := n.(*ast.TypeSpec)
		if !ok {
			continue
		}

		// Token files are 1-based, while ast files are 0-based.
		// We need to adjust the position to match the token file.
		filePos := tSpec.Pos() - astFile.FileStart + token.Pos(tokenFile.Base())

		if filePos == namedType.Obj().Pos() {
			return tSpec, true
		}
	}

	return nil, false
}

// FieldName returns the name of the field. If the field has a name, it returns that name.
// If the field is embedded and it can be converted to an identifier, it returns the name of the identifier.
// If it doesn't have a name and can't be converted to an identifier, it returns an empty string.
func FieldName(field *ast.Field) string {
	if len(field.Names) > 0 && field.Names[0] != nil {
		return field.Names[0].Name
	}

	switch typ := field.Type.(type) {
	case *ast.Ident:
		return typ.Name
	case *ast.StarExpr:
		if ident, ok := typ.X.(*ast.Ident); ok {
			return ident.Name
		}
	}

	return ""
}

func getFilesForType(pass *analysis.Pass, ident *ast.Ident) (*token.File, *ast.File) {
	namedType, ok := pass.TypesInfo.TypeOf(ident).(*types.Named)
	if !ok {
		return nil, nil
	}

	tokenFile := pass.Fset.File(namedType.Obj().Pos())

	for _, astFile := range pass.Files {
		if astFile.FileStart == token.Pos(tokenFile.Base()) {
			return tokenFile, astFile
		}
	}

	return tokenFile, nil
}

func isInPassPackage(pass *analysis.Pass, namedType *types.Named) bool {
	return namedType.Obj().Pkg() != nil && namedType.Obj().Pkg() == pass.Pkg
}

// TypeAwareMarkerCollectionForField collects the markers for a given field into a single markers.MarkerSet.
// If the field has a type that is not a basic type (i.e a custom type) then it will also gather any markers from
// the type and include them in the markers.MarkerSet that is returned.
// It will look through *ast.StarExpr to the underlying type.
// Markers on the type will always come before markers on the field in the list of markers for an identifier.
func TypeAwareMarkerCollectionForField(pass *analysis.Pass, markersAccess markers.Markers, field *ast.Field) markers.MarkerSet {
	markers := markersAccess.FieldMarkers(field)

	var underlyingType ast.Expr

	switch t := field.Type.(type) {
	case *ast.Ident:
		underlyingType = t
	case *ast.StarExpr:
		underlyingType = t.X
	default:
		return markers
	}

	ident, ok := underlyingType.(*ast.Ident)
	if !ok {
		return markers
	}

	if IsBasicType(pass, ident) {
		return markers
	}

	typeSpec, ok := LookupTypeSpec(pass, ident)
	if !ok {
		return markers
	}

	typeMarkers := markersAccess.TypeMarkers(typeSpec)
	typeMarkers.Insert(markers.UnsortedList()...)

	return typeMarkers
}

// IsArrayTypeOrAlias checks if the field type is an array type or an alias to an array type.
func IsArrayTypeOrAlias(pass *analysis.Pass, field *ast.Field) bool {
	if _, ok := field.Type.(*ast.ArrayType); ok {
		return true
	}

	if ident, ok := field.Type.(*ast.Ident); ok {
		typeOf := pass.TypesInfo.TypeOf(ident)
		if typeOf == nil {
			return false
		}

		return isArrayType(typeOf)
	}

	return false
}

// IsObjectList checks if the field represents a list of objects (not primitives).
func IsObjectList(pass *analysis.Pass, field *ast.Field) bool {
	if arrayType, ok := field.Type.(*ast.ArrayType); ok {
		return inspectType(pass, arrayType.Elt)
	}

	if ident, ok := field.Type.(*ast.Ident); ok {
		typeOf := pass.TypesInfo.TypeOf(ident)
		if typeOf == nil {
			return false
		}

		return isObjectListFromType(typeOf)
	}

	return false
}

// IsByteArray checks if the field type is a byte array or an alias to a byte array.
func IsByteArray(pass *analysis.Pass, field *ast.Field) bool {
	if arrayType, ok := field.Type.(*ast.ArrayType); ok {
		if ident, ok := arrayType.Elt.(*ast.Ident); ok && types.Identical(pass.TypesInfo.TypeOf(ident), types.Typ[types.Byte]) {
			return true
		}
	}

	if ident, ok := field.Type.(*ast.Ident); ok {
		typeOf := pass.TypesInfo.TypeOf(ident)
		if typeOf == nil {
			return false
		}

		switch typeOf := typeOf.(type) {
		case *types.Alias:
			if sliceType, ok := typeOf.Underlying().(*types.Slice); ok {
				return types.Identical(sliceType.Elem(), types.Typ[types.Byte])
			}
		case *types.Named:
			if sliceType, ok := typeOf.Underlying().(*types.Slice); ok {
				return types.Identical(sliceType.Elem(), types.Typ[types.Byte])
			}
		}
	}

	return false
}

func isArrayType(t types.Type) bool {
	if aliasType, ok := t.(*types.Alias); ok {
		return isArrayType(aliasType.Underlying())
	}

	if namedType, ok := t.(*types.Named); ok {
		return isArrayType(namedType.Underlying())
	}

	if _, ok := t.(*types.Slice); ok {
		return true
	}

	return false
}

func isObjectListFromType(t types.Type) bool {
	if aliasType, ok := t.(*types.Alias); ok {
		return isObjectListFromType(aliasType.Underlying())
	}

	if namedType, ok := t.(*types.Named); ok {
		return isObjectListFromType(namedType.Underlying())
	}

	if sliceType, ok := t.(*types.Slice); ok {
		return !isTypeBasic(sliceType.Elem())
	}

	return false
}

func inspectType(pass *analysis.Pass, expr ast.Expr) bool {
	switch elementType := expr.(type) {
	case *ast.Ident:
		return !isBasicOrAliasToBasic(pass, elementType)
	case *ast.StarExpr:
		return inspectType(pass, elementType.X)
	case *ast.ArrayType:
		return inspectType(pass, elementType.Elt)
	case *ast.SelectorExpr:
		return true
	}

	return false
}

func isBasicOrAliasToBasic(pass *analysis.Pass, ident *ast.Ident) bool {
	typeOf := pass.TypesInfo.TypeOf(ident)
	if typeOf == nil {
		return false
	}

	return isTypeBasic(typeOf)
}

func isTypeBasic(t types.Type) bool {
	// Direct basic type
	if _, ok := t.(*types.Basic); ok {
		return true
	}

	// Handle type aliases (type T = U)
	if aliasType, ok := t.(*types.Alias); ok {
		return isTypeBasic(aliasType.Underlying())
	}

	// Handle defined types (type T U)
	if namedType, ok := t.(*types.Named); ok {
		return isTypeBasic(namedType.Underlying())
	}

	return false
}
