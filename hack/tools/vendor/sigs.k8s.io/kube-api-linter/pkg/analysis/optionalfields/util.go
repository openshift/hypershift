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
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/tools/go/analysis"
	"k8s.io/utils/ptr"
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

// isZeroValueValid determines whether the zero value of the field is valid per the validation markers.
// For example, if the string has a minimum length greater than 0, the zero value is not valid.
// Or if the minimum value of an integer field is greater than 0, the zero value is not valid.
// For structs, for the zero value to be valid, all fields within the struct that would not be omitted must accept their zero values.
// The second return value indicates whether the field validation is complete. Complete validation means that we are certain whether or not the zero value is valid.
// Incomplete validation means that if additional validation were added (e.g. to add a min length to a string), the zero value might become invalid.
func isZeroValueValid(pass *analysis.Pass, field *ast.Field, typeExpr ast.Expr, markersAccess markershelper.Markers) (bool, bool) {
	_, underlyingType := isStarExpr(typeExpr)

	switch t := underlyingType.(type) {
	case *ast.StructType:
		// For structs, we have to check if there are any non-omitted fields, that do not accept a zero value.
		return isStructZeroValueValid(pass, field, t, markersAccess)
	case *ast.Ident:
		return isIdentZeroValueValid(pass, field, t, markersAccess)
	case *ast.MapType:
		return isMapZeroValueValid(field, markersAccess)
	case *ast.ArrayType:
		// For arrays, we can use a zero value if the array is not required to have a minimum number of items.
		return isArrayZeroValueValid(field, t, markersAccess)
	case *ast.StarExpr:
		return isZeroValueValid(pass, field, t.X, markersAccess)
	}

	// We don't know what the type is so can't assert the zero value is valid.
	return false, false
}

// isStructZeroValueValid checks if the zero value of a struct is valid.
// It checks if all non-omitted fields within the struct accept their zero values.
// It also checks if the struct has a minProperties marker, and if so, whether the number of non-omitted fields is greater than or equal to the minProperties value.
func isStructZeroValueValid(pass *analysis.Pass, field *ast.Field, structType *ast.StructType, markersAccess markershelper.Markers) (bool, bool) {
	if structType == nil {
		return false, false
	}

	jsonTagInfo, ok := pass.ResultOf[extractjsontags.Analyzer].(extractjsontags.StructFieldTags)
	if !ok {
		panic("could not get struct field tags from pass result")
	}

	zeroValueValid, nonOmittedFields := areStructFieldZeroValuesValid(pass, structType, markersAccess, jsonTagInfo)

	markerSet := utils.TypeAwareMarkerCollectionForField(pass, markersAccess, field)

	minProperties, err := getMarkerNumericValueByName[int](markerSet, markers.KubebuilderMinPropertiesMarker)
	if err != nil && !errors.Is(err, errMarkerMissingValue) {
		pass.Reportf(field.Pos(), "struct %s has an invalid minProperties marker: %v", utils.FieldName(field), err)
		return false, false
	}

	if minProperties != nil && *minProperties > nonOmittedFields {
		// The struct requires more properties than would be marshalled in the zero value of the struct.
		zeroValueValid = false
	}

	var completeStructValidation = true
	if minProperties == nil && nonOmittedFields == 0 {
		// If the struct has no non-omitted fields, then the zero value of the struct is `{}`.
		// This generally means that the validation is incomplete as the difference between omitting the field and not omitting is not clear.
		completeStructValidation = false
	}

	return zeroValueValid, completeStructValidation
}

// areStructFieldZeroValuesValid checks if all non-omitted fields within a struct accept their zero values.
func areStructFieldZeroValuesValid(pass *analysis.Pass, structType *ast.StructType, markersAccess markershelper.Markers, jsonTagInfo extractjsontags.StructFieldTags) (bool, int) {
	zeroValueValid := true
	nonOmittedFields := 0

	for _, field := range structType.Fields.List {
		fieldTagInfo := jsonTagInfo.FieldTags(field)

		if fieldTagInfo.OmitEmpty {
			// If the field is omitted, we can use a zero value.
			// For structs, if they aren't a pointer another error will be raised.
			continue
		}

		nonOmittedFields++

		validValue, _ := isZeroValueValid(pass, field, field.Type, markersAccess)

		// If either value is false then the collected values will be false.
		zeroValueValid = zeroValueValid && validValue
	}

	return zeroValueValid, nonOmittedFields
}

// isIdentZeroValueValid checks if the zero value of an identifier is valid.
func isIdentZeroValueValid(pass *analysis.Pass, field *ast.Field, ident *ast.Ident, markersAccess markershelper.Markers) (bool, bool) {
	if ident == nil {
		return false, false
	}

	// Check if the identifier is a known type that can have a zero value.
	switch {
	case isStringIdent(ident):
		return isStringZeroValueValid(field, markersAccess)
	case isIntegerIdent(ident):
		return isNumericZeroValueValid[int](pass, field, markersAccess)
	case isFloatIdent(ident):
		return isNumericZeroValueValid[float64](pass, field, markersAccess)
	case isBoolIdent(ident):
		// For bool, we can always use a zero value.
		return true, true
	}

	// If the ident isn't one of the above, check the underlying type spec.
	typeSpec, ok := utils.LookupTypeSpec(pass, ident)
	if !ok {
		return false, false
	}

	return isZeroValueValid(pass, field, typeSpec.Type, markersAccess)
}

// isStringZeroValueValid checks if a string field can have a zero value.
// This would be true when either there is no minimum length marker, or when the minimmum length marker is set to 0.
func isStringZeroValueValid(field *ast.Field, markersAccess markershelper.Markers) (bool, bool) {
	fieldMarkers := markersAccess.FieldMarkers(field)

	if stringFieldIsEnum(fieldMarkers) {
		return enumFieldAllowsEmpty(fieldMarkers), true
	}

	hasMinLengthMarker := fieldMarkers.Has(markers.KubebuilderMinLengthMarker)
	minLengthMarkerIsZero := fieldMarkers.HasWithValue(fmt.Sprintf("%s=0", markers.KubebuilderMinLengthMarker))

	return !hasMinLengthMarker || minLengthMarkerIsZero, hasMinLengthMarker
}

// isMapZeroValueValid checks if a map field can have a zero value.
// For maps, this means there is no minProperties marker, or the minProperties marker is set to 0.
func isMapZeroValueValid(field *ast.Field, markersAccess markershelper.Markers) (bool, bool) {
	fieldMarkers := markersAccess.FieldMarkers(field)

	hasMinPropertiesMarker := fieldMarkers.Has(markers.KubebuilderMinPropertiesMarker)
	minPropertiesMarkerIsZero := fieldMarkers.HasWithValue(fmt.Sprintf("%s=0", markers.KubebuilderMinPropertiesMarker))

	return !hasMinPropertiesMarker || minPropertiesMarkerIsZero, hasMinPropertiesMarker
}

// isArrayZeroValueValid checks if an array field can have a zero value.
func isArrayZeroValueValid(field *ast.Field, arrayType *ast.ArrayType, markersAccess markershelper.Markers) (bool, bool) {
	// Arrays of bytes are special cased and treated as strings.
	if ident, ok := arrayType.Elt.(*ast.Ident); ok && ident.Name == "byte" {
		return isStringZeroValueValid(field, markersAccess)
	}

	fieldMarkers := markersAccess.FieldMarkers(field)

	// For arrays, we can use a zero value if the array is not required to have a minimum number of items.
	minItems, err := getMarkerNumericValueByName[int](fieldMarkers, markers.KubebuilderMinItemsMarker)
	if err != nil && !errors.Is(err, errMarkerMissingValue) {
		return false, false
	}

	return minItems == nil || *minItems == 0, minItems != nil
}

func stringFieldIsEnum(fieldMarkers markershelper.MarkerSet) bool {
	// Check if the field has a kubebuilder enum marker.
	return fieldMarkers.Has(markers.KubebuilderEnumMarker)
}

func enumFieldAllowsEmpty(fieldMarkers markershelper.MarkerSet) bool {
	// Check if the field has a kubebuilder enum marker with an empty value.
	enumMarker := fieldMarkers.Get(markers.KubebuilderEnumMarker)
	if len(enumMarker) == 0 {
		return false
	}

	for _, marker := range enumMarker {
		return slices.Contains(strings.Split(marker.Expressions[""], ";"), "\"\"")
	}

	return false
}

// number is a type constraint for numeric types.
// It allows us to create a generic extraction function for numeric values from markers.
type number interface {
	int | float64
}

// isIntegerZeroValueValid checks if an integer field can have a zero value.
//
//nolint:cyclop
func isNumericZeroValueValid[N number](pass *analysis.Pass, field *ast.Field, markersAccess markershelper.Markers) (bool, bool) {
	fieldMarkers := markersAccess.FieldMarkers(field)

	minimum, err := getMarkerNumericValueByName[N](fieldMarkers, markers.KubebuilderMinimumMarker)
	if err != nil && !errors.Is(err, errMarkerMissingValue) {
		pass.Reportf(field.Pos(), "field %s has an invalid minimum marker: %v", utils.FieldName(field), err)
		return false, false
	}

	maximum, err := getMarkerNumericValueByName[N](fieldMarkers, markers.KubebuilderMaximumMarker)
	if err != nil && !errors.Is(err, errMarkerMissingValue) {
		pass.Reportf(field.Pos(), "field %s has an invalid maximum marker: %v", utils.FieldName(field), err)
		return false, false
	}

	hasGreaterThanZeroMinimum := minimum != nil && *minimum >= 0
	hasLessThanZeroMaximum := maximum != nil && *maximum <= 0
	hasCompleteRange := minimum != nil && maximum != nil && *minimum <= *maximum

	return ptr.Deref(minimum, -1) <= 0 && ptr.Deref(maximum, 1) >= 0, hasCompleteRange || hasGreaterThanZeroMinimum || hasLessThanZeroMaximum
}

// getMarkerNumericValueByName extracts the numeric value from the first instance of the marker with the given name.
// Works for markers like MaxLength, MinLength, etc.
func getMarkerNumericValueByName[N number](marker markershelper.MarkerSet, markerName string) (*N, error) {
	markerList := marker.Get(markerName)
	if len(markerList) == 0 {
		return nil, errMarkerMissingValue
	}

	markerValue, err := getMarkerNumericValue[N](markerList[0])
	if err != nil {
		return nil, fmt.Errorf("error getting marker value: %w", err)
	}

	return &markerValue, nil
}

// getMarkerNumericValue extracts a numeric value from the default value of a marker.
// Works for markers like MaxLength, MinLength, etc.
func getMarkerNumericValue[N number](marker markershelper.Marker) (N, error) {
	rawValue, ok := marker.Expressions[""]
	if !ok {
		return N(0), errMarkerMissingValue
	}

	value, err := strconv.ParseFloat(rawValue, 64)
	if err != nil {
		return N(0), fmt.Errorf("error converting value to number: %w", err)
	}

	return N(value), nil
}

// isIntegerIdent checks if the identifier is one of the integer types.
func isIntegerIdent(ident *ast.Ident) bool {
	switch ident.Name {
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64":
		return true
	}

	return false
}

// isStringIdent checks if the identifier is a string type.
func isStringIdent(ident *ast.Ident) bool {
	return ident.Name == "string"
}

// isBoolIdent checks if the identifier is a boolean type.
func isBoolIdent(ident *ast.Ident) bool {
	return ident.Name == "bool"
}

// isFloatIdent checks if the identifier is one of the float types.
func isFloatIdent(ident *ast.Ident) bool {
	return ident.Name == "float32" || ident.Name == "float64"
}
