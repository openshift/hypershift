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
	"errors"
	"fmt"
	"go/ast"
	"go/types"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/tools/go/analysis"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/kube-api-linter/pkg/analysis/helpers/extractjsontags"
	markershelper "sigs.k8s.io/kube-api-linter/pkg/analysis/helpers/markers"
	"sigs.k8s.io/kube-api-linter/pkg/markers"
)

var (
	errMarkerMissingValue = errors.New("marker does not have a value")
)

// IsZeroValueValid determines whether the zero value of the field is valid per the validation markers.
// For example, if the string has a minimum length greater than 0, the zero value is not valid.
// Or if the minimum value of an integer field is greater than 0, the zero value is not valid.
// For structs, for the zero value to be valid, all fields within the struct that would not be omitted must accept their zero values.
// The second return value indicates whether the field validation is complete. Complete validation means that we are certain whether or not the zero value is valid.
// Incomplete validation means that if additional validation were added (e.g. to add a min length to a string), the zero value might become invalid.
func IsZeroValueValid(pass *analysis.Pass, field *ast.Field, typeExpr ast.Expr, markersAccess markershelper.Markers, considerOmitzero bool, qualifiedFieldName string) (bool, bool) {
	underlyingType := getUnderlyingType(typeExpr)

	switch t := underlyingType.(type) {
	case *ast.StructType:
		// For structs, we have to check if there are any non-omitted fields, that do not accept a zero value.
		return isStructZeroValueValid(pass, field, t, markersAccess, considerOmitzero, qualifiedFieldName)
	case *ast.Ident:
		return isIdentZeroValueValid(pass, field, t, markersAccess, considerOmitzero, qualifiedFieldName)
	case *ast.MapType:
		return isMapZeroValueValid(pass, field, markersAccess)
	case *ast.ArrayType:
		// For arrays, we can use a zero value if the array is not required to have a minimum number of items.
		return isArrayZeroValueValid(pass, field, t, markersAccess)
	case *ast.StarExpr:
		return IsZeroValueValid(pass, field, t.X, markersAccess, considerOmitzero, qualifiedFieldName)
	case *ast.SelectorExpr:
		// For qualified identifiers (e.g., corev1.ResourceList), use type info
		// since we cannot look up the AST for external packages.
		return isSelectorExprZeroValueValid(pass, field, t, markersAccess, qualifiedFieldName)
	}

	// We don't know what the type is so can't assert the zero value is valid.
	return false, false
}

// isSelectorExprZeroValueValid checks if a qualified identifier (external package type) has a valid zero value.
// It uses Go's type system to determine the underlying type.
func isSelectorExprZeroValueValid(pass *analysis.Pass, field *ast.Field, selector *ast.SelectorExpr, markersAccess markershelper.Markers, qualifiedFieldName string) (bool, bool) {
	typeOf := pass.TypesInfo.TypeOf(selector)
	if typeOf == nil {
		return false, false
	}

	underlying := typeOf.Underlying()

	switch t := underlying.(type) {
	case *types.Slice:
		return isArrayZeroValueValidFromMarkers(pass, field, markersAccess)
	case *types.Map:
		return isMapZeroValueValid(pass, field, markersAccess)
	case *types.Basic:
		return isBasicTypeZeroValueValid(pass, field, t, markersAccess, qualifiedFieldName)
	case *types.Struct:
		// For external structs, inspect fields via Go's type system.
		return isExternalStructZeroValueValid(pass, field, t, markersAccess)
	}

	return false, false
}

// isBasicTypeZeroValueValid checks if a basic type (string, int, float, bool) has a valid zero value.
// This is used for external package type aliases to basic types.
func isBasicTypeZeroValueValid(pass *analysis.Pass, field *ast.Field, basicType *types.Basic, markersAccess markershelper.Markers, qualifiedFieldName string) (bool, bool) {
	//nolint:exhaustive // We only handle common basic types; other types fall through to default.
	switch basicType.Kind() {
	case types.String:
		return isStringZeroValueValid(pass, field, markersAccess)
	case types.Bool:
		// For bool, we can always use a zero value (false is valid).
		return true, true
	case types.Int, types.Int8, types.Int16, types.Int32, types.Int64,
		types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64:
		return isNumericZeroValueValid[int](pass, field, markersAccess, qualifiedFieldName)
	case types.Float32, types.Float64:
		return isNumericZeroValueValid[float64](pass, field, markersAccess, qualifiedFieldName)
	default:
		// For other basic types (complex, unsafe pointer, untyped, etc.), we can't determine validity.
		return false, false
	}
}

// isExternalStructZeroValueValid checks if an external struct type has a valid zero value.
// It inspects the struct fields via Go's type system to count non-omitted fields,
// similar to how local structs are validated.
func isExternalStructZeroValueValid(pass *analysis.Pass, field *ast.Field, structType *types.Struct, markersAccess markershelper.Markers) (bool, bool) {
	fieldMarkers := TypeAwareMarkerCollectionForField(pass, markersAccess, field)

	minProperties, err := GetMinProperties(fieldMarkers)
	if err != nil {
		pass.Reportf(field.Pos(), "struct %s has an invalid minProperties marker: %v", FieldName(field), err)
		return false, false
	}

	// Count non-omitted fields by inspecting the struct's fields via the type system.
	nonOmittedFields := countNonOmittedFieldsInExternalStruct(structType)

	zeroValueValid := minProperties == nil || *minProperties <= nonOmittedFields

	// All fields have omitempty/omitzero - zero value is {} but validation is incomplete.
	completeValidation := minProperties != nil || nonOmittedFields > 0

	return zeroValueValid, completeValidation
}

// countNonOmittedFieldsInExternalStruct counts fields in an external struct
// that would be marshalled in the zero value (i.e., fields without omitempty or omitzero).
func countNonOmittedFieldsInExternalStruct(structType *types.Struct) int {
	count := 0

	for i := range structType.NumFields() {
		f := structType.Field(i)

		// Skip unexported and embedded fields.
		if !f.Exported() || f.Embedded() {
			continue
		}

		tag := structType.Tag(i)
		if isFieldOmittedByTag(tag) {
			continue
		}

		count++
	}

	return count
}

// isFieldOmittedByTag checks if a struct field would be omitted in the zero value
// based on its struct tag. It checks for json:"-", omitempty, omitzero, and inline.
func isFieldOmittedByTag(tag string) bool {
	jsonTag, ok := reflect.StructTag(tag).Lookup("json")
	if !ok {
		return false
	}

	// Ignored field (json:"-")
	if jsonTag == "-" {
		return true
	}

	parts := strings.Split(jsonTag, ",")
	for _, part := range parts[1:] {
		if part == "omitempty" || part == "omitzero" || part == "inline" {
			return true
		}
	}

	return false
}

// isArrayZeroValueValidFromMarkers checks if an array/slice field can have a zero value based on markers only.
// This is used for external package types where we can't inspect the AST.
func isArrayZeroValueValidFromMarkers(pass *analysis.Pass, field *ast.Field, markersAccess markershelper.Markers) (bool, bool) {
	fieldMarkers := TypeAwareMarkerCollectionForField(pass, markersAccess, field)

	hasMinItemsMarker := fieldMarkers.Has(markers.KubebuilderMinItemsMarker)
	minItemsMarkerIsZero := fieldMarkers.HasWithValue(fmt.Sprintf("%s=0", markers.KubebuilderMinItemsMarker))

	return !hasMinItemsMarker || minItemsMarkerIsZero, hasMinItemsMarker
}

// getUnderlyingType returns the underlying type of the expression.
// If the expression is a pointer, it returns the expression inside the pointer.
func getUnderlyingType(expr ast.Expr) ast.Expr {
	if ptrType, ok := expr.(*ast.StarExpr); ok {
		return ptrType.X
	}

	return expr
}

// GetTypeMarkerValue returns the value of the kubebuilder Type marker for a field.
// Returns empty string if no Type marker is present.
// The Type marker indicates how the field serializes (e.g., "string", "number", "object").
func GetTypeMarkerValue(pass *analysis.Pass, field *ast.Field, markersAccess markershelper.Markers) string {
	fieldMarkers := TypeAwareMarkerCollectionForField(pass, markersAccess, field)
	typeMarkers := fieldMarkers.Get(markers.KubebuilderTypeMarker)

	for _, typeMarker := range typeMarkers {
		// The value might be "string" (with quotes) or string (without quotes)
		typeValue := strings.Trim(typeMarker.Payload.Value, `"`)
		if typeValue != "" {
			return typeValue
		}
	}

	return ""
}

// isStructZeroValueValid checks if the zero value of a struct is valid.
// It checks if all non-omitted fields within the struct accept their zero values.
// It also checks if the struct has a minProperties marker, and if so, whether the number of non-omitted fields is greater than or equal to the minProperties value.
// Special case: If the struct has Type=string marker with string validation markers (MinLength/MaxLength),
// treat it as a string for validation purposes (e.g., for structs with custom marshalling).
func isStructZeroValueValid(pass *analysis.Pass, field *ast.Field, structType *ast.StructType, markersAccess markershelper.Markers, considerOmitzero bool, qualifiedFieldName string) (bool, bool) {
	if structType == nil {
		return false, false
	}

	// Check if this struct should be validated as a string (Type=string marker).
	// This handles structs with custom marshalling that serialize as strings.
	if GetTypeMarkerValue(pass, field, markersAccess) == stringTypeName {
		// Use string validation logic instead of struct validation logic.
		// This ensures that string-specific validation markers (MinLength, MaxLength, Pattern)
		// are properly evaluated for structs that marshal as strings.
		return isStringZeroValueValid(pass, field, markersAccess)
	}

	jsonTagInfo, ok := pass.ResultOf[extractjsontags.Analyzer].(extractjsontags.StructFieldTags)
	if !ok {
		panic("could not get struct field tags from pass result")
	}

	zeroValueValid, nonOmittedFields := areStructFieldZeroValuesValid(pass, structType, markersAccess, jsonTagInfo, considerOmitzero, qualifiedFieldName)

	markerSet := TypeAwareMarkerCollectionForField(pass, markersAccess, field)

	structZeroValid, completeStructValidation := checkStructMinProperties(pass, field, markerSet, structType, markersAccess, nonOmittedFields)
	if !structZeroValid {
		zeroValueValid = false
	}

	return zeroValueValid, completeStructValidation
}

// checkStructMinProperties checks if the struct's zero value satisfies min-properties constraints.
// It considers both explicit minProperties markers and union markers (ExactlyOneOf/AtLeastOneOf),
// which implicitly require at least one field to be set.
// Returns (zeroValueValid, completeValidation).
func checkStructMinProperties(pass *analysis.Pass, field *ast.Field, markerSet markershelper.MarkerSet, structType *ast.StructType, markersAccess markershelper.Markers, nonOmittedFields int) (bool, bool) {
	minProperties, err := GetMinProperties(markerSet)
	if err != nil {
		pass.Reportf(field.Pos(), "struct %s has an invalid minProperties marker: %v", FieldName(field), err)
		return false, false
	}

	// Union markers (ExactlyOneOf/AtLeastOneOf) implicitly require at least one field,
	// equivalent to minProperties=1.
	structMarkerSet := markersAccess.StructMarkers(structType)
	if minProperties == nil && (structMarkerSet.Has(markers.KubebuilderExactlyOneOf) || structMarkerSet.Has(markers.KubebuilderAtLeastOneOfMarker)) {
		minProperties = ptr.To(1)
	}

	zeroValueValid := minProperties == nil || *minProperties <= nonOmittedFields
	// If the struct has no non-omitted fields and no min-properties constraint, then the zero value
	// is `{}` and the validation is incomplete.
	completeValidation := minProperties != nil || nonOmittedFields > 0

	return zeroValueValid, completeValidation
}

// areStructFieldZeroValuesValid checks if all non-omitted fields within a struct accept their zero values.
//
//nolint:cyclop
func areStructFieldZeroValuesValid(pass *analysis.Pass, structType *ast.StructType, markersAccess markershelper.Markers, jsonTagInfo extractjsontags.StructFieldTags, considerOmitzero bool, qualifiedFieldName string) (bool, int) {
	zeroValueValid := true
	nonOmittedFields := 0

	for _, field := range structType.Fields.List {
		fieldRequired := IsFieldRequired(field, markersAccess)
		fieldTagInfo := jsonTagInfo.FieldTags(field)
		isStruct := IsStructType(pass, field.Type)
		isPointer := IsPointer(field.Type)

		// Assume the field has omitempty.
		// Then the zero value (omitted) for a required field is not valid, and for an optional field it is valid.
		validValue := !fieldRequired

		// non-omitted fields are required fields or fields without an omitempty tag or struct fields without omitzero tag (if valid omitzero policy is set)
		// This allows us to count them towards the min-properties count in the parent function.
		switch {
		case fieldRequired:
			nonOmittedFields++
		case isStruct && considerOmitzero && fieldTagInfo.OmitZero:
			// struct with omitzero field should be omitted.
		case !fieldTagInfo.OmitEmpty:
			nonOmittedFields++
		}

		// When the field is not omitted, we need to check if the zero value is valid (required or not).
		switch {
		case isStruct && considerOmitzero && fieldTagInfo.OmitZero:
		case isPointer:
			// A field that is a pointer and does not have an omitempty would marshal as null.
			// This is silently dropped by the API server, or is accepted as a valid value with +nullable.
			// If the field does have omitempty, then the zero value is valid based on the requiredness of the field.
		case !fieldTagInfo.OmitEmpty:
			validValue, _ = IsZeroValueValid(pass, field, field.Type, markersAccess, considerOmitzero, qualifiedFieldName)
		}

		// If either value is false then the collected values will be false.
		zeroValueValid = zeroValueValid && validValue
	}

	return zeroValueValid, nonOmittedFields
}

// isIdentZeroValueValid checks if the zero value of an identifier is valid.
func isIdentZeroValueValid(pass *analysis.Pass, field *ast.Field, ident *ast.Ident, markersAccess markershelper.Markers, considerOmitzero bool, qualifiedFieldName string) (bool, bool) {
	if ident == nil {
		return false, false
	}

	// Check if the identifier is a known type that can have a zero value.
	switch {
	case isStringIdent(ident):
		return isStringZeroValueValid(pass, field, markersAccess)
	case isIntegerIdent(ident):
		return isNumericZeroValueValid[int](pass, field, markersAccess, qualifiedFieldName)
	case isFloatIdent(ident):
		return isNumericZeroValueValid[float64](pass, field, markersAccess, qualifiedFieldName)
	case isBoolIdent(ident):
		// For bool, we can always use a zero value.
		return true, true
	}

	// If the ident isn't one of the above, check the underlying type spec.
	typeSpec, ok := LookupTypeSpec(pass, ident)
	if !ok {
		return false, false
	}

	return IsZeroValueValid(pass, field, typeSpec.Type, markersAccess, considerOmitzero, qualifiedFieldName)
}

// isStringZeroValueValid checks if a string field can have a zero value.
// This would be true when either there is no minimum length marker, or when the minimmum length marker is set to 0.
func isStringZeroValueValid(pass *analysis.Pass, field *ast.Field, markersAccess markershelper.Markers) (bool, bool) {
	fieldMarkers := TypeAwareMarkerCollectionForField(pass, markersAccess, field)

	if stringFieldIsEnum(fieldMarkers) {
		return enumFieldAllowsEmpty(fieldMarkers), true
	}

	hasMinLengthMarker := fieldMarkers.Has(markers.KubebuilderMinLengthMarker)
	minLengthMarkerIsZero := fieldMarkers.HasWithValue(fmt.Sprintf("%s=0", markers.KubebuilderMinLengthMarker))

	return !hasMinLengthMarker || minLengthMarkerIsZero, hasMinLengthMarker
}

// isMapZeroValueValid checks if a map field can have a zero value.
// For maps, this means there is no minProperties marker, or the minProperties marker is set to 0.
func isMapZeroValueValid(pass *analysis.Pass, field *ast.Field, markersAccess markershelper.Markers) (bool, bool) {
	fieldMarkers := TypeAwareMarkerCollectionForField(pass, markersAccess, field)

	hasMinPropertiesMarker := fieldMarkers.Has(markers.KubebuilderMinPropertiesMarker)
	minPropertiesMarkerIsZero := fieldMarkers.HasWithValue(fmt.Sprintf("%s=0", markers.KubebuilderMinPropertiesMarker))

	return !hasMinPropertiesMarker || minPropertiesMarkerIsZero, hasMinPropertiesMarker
}

// isArrayZeroValueValid checks if an array field can have a zero value.
func isArrayZeroValueValid(pass *analysis.Pass, field *ast.Field, arrayType *ast.ArrayType, markersAccess markershelper.Markers) (bool, bool) {
	// Arrays of bytes are special cased and treated as strings.
	if ident, ok := arrayType.Elt.(*ast.Ident); ok && ident.Name == "byte" {
		return isStringZeroValueValid(pass, field, markersAccess)
	}

	fieldMarkers := TypeAwareMarkerCollectionForField(pass, markersAccess, field)

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

	for _, marker := range enumMarker {
		return slices.Contains(strings.Split(marker.Payload.Value, ";"), "\"\"")
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
func isNumericZeroValueValid[N number](pass *analysis.Pass, field *ast.Field, markersAccess markershelper.Markers, qualifiedFieldName string) (bool, bool) {
	fieldMarkers := TypeAwareMarkerCollectionForField(pass, markersAccess, field)

	minimum, err := getMarkerNumericValueByName[N](fieldMarkers, markers.KubebuilderMinimumMarker)
	if err != nil && !errors.Is(err, errMarkerMissingValue) {
		pass.Reportf(field.Pos(), "field %s has an invalid minimum marker: %v", qualifiedFieldName, err)
		return false, false
	}

	maximum, err := getMarkerNumericValueByName[N](fieldMarkers, markers.KubebuilderMaximumMarker)
	if err != nil && !errors.Is(err, errMarkerMissingValue) {
		pass.Reportf(field.Pos(), "field %s has an invalid maximum marker: %v", qualifiedFieldName, err)
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
	if marker.Payload.Value == "" {
		return N(0), errMarkerMissingValue
	}

	rawValue := marker.Payload.Value

	value, err := strconv.ParseFloat(rawValue, 64)
	if err != nil {
		return N(0), fmt.Errorf("error converting value to number: %w", err)
	}

	return N(value), nil
}

// GetTypedZeroValue returns the zero value for a given type as a string representation.
func GetTypedZeroValue(pass *analysis.Pass, expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return getIdentZeroValue(pass, t)
	case *ast.StructType:
		return getStructZeroValue(pass, t)
	case *ast.ArrayType:
		return "[]"
	case *ast.MapType:
		return "{}"
	case *ast.SelectorExpr:
		return getSelectorExprZeroValue(pass, t)
	default:
		return ""
	}
}

// getSelectorExprZeroValue returns the zero value for a qualified identifier (external package type).
func getSelectorExprZeroValue(pass *analysis.Pass, selector *ast.SelectorExpr) string {
	typeOf := pass.TypesInfo.TypeOf(selector)
	if typeOf == nil {
		return ""
	}

	underlying := typeOf.Underlying()

	switch t := underlying.(type) {
	case *types.Slice:
		return "[]"
	case *types.Map:
		return "{}"
	case *types.Struct:
		return getExternalStructZeroValue(t)
	case *types.Basic:
		return getBasicTypeZeroValue(t)
	default:
		return ""
	}
}

// getExternalStructZeroValue returns the zero value for an external struct type.
// It constructs a json-like representation including only non-omitted fields.
func getExternalStructZeroValue(structType *types.Struct) string {
	value := "{"

	for i := range structType.NumFields() {
		f := structType.Field(i)

		if !f.Exported() || f.Embedded() {
			continue
		}

		tag := structType.Tag(i)
		if isFieldOmittedByTag(tag) {
			continue
		}

		jsonName := getJSONFieldName(tag, f.Name())
		zeroVal := getTypesTypeZeroValue(f.Type())
		value += fmt.Sprintf("%q: %s, ", jsonName, zeroVal)
	}

	value = strings.TrimSuffix(value, ", ")
	value += "}"

	return value
}

// getJSONFieldName extracts the JSON field name from a struct tag.
// Falls back to the Go field name if no json tag is present.
func getJSONFieldName(tag string, fieldName string) string {
	jsonTag, ok := reflect.StructTag(tag).Lookup("json")
	if !ok || jsonTag == "" {
		return fieldName
	}

	parts := strings.Split(jsonTag, ",")
	if parts[0] != "" {
		return parts[0]
	}

	return fieldName
}

// getTypesTypeZeroValue returns the zero value string for a types.Type.
func getTypesTypeZeroValue(t types.Type) string {
	underlying := t.Underlying()

	switch basicType := underlying.(type) {
	case *types.Basic:
		return getBasicTypeZeroValue(basicType)
	case *types.Slice:
		return "[]"
	case *types.Map:
		return "{}"
	case *types.Struct:
		return "{}"
	case *types.Pointer:
		return "null"
	default:
		return ""
	}
}

// getBasicTypeZeroValue returns the zero value for a basic type.
func getBasicTypeZeroValue(basicType *types.Basic) string {
	//nolint:exhaustive // We only handle common basic types; other types fall through to default.
	switch basicType.Kind() {
	case types.String:
		return `""`
	case types.Bool:
		return "false"
	case types.Int, types.Int8, types.Int16, types.Int32, types.Int64,
		types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64:
		return "0"
	case types.Float32, types.Float64:
		return "0.0"
	default:
		return ""
	}
}

// getIdentZeroValue returns the zero value for a given identifier as a string representation.
// Where the ident is an alias for a type, it will look up the type spec to get the underlying type
// and return the zero value for that type.
func getIdentZeroValue(pass *analysis.Pass, ident *ast.Ident) string {
	switch {
	case isIntegerIdent(ident):
		return "0"
	case isStringIdent(ident):
		return `""`
	case isBoolIdent(ident):
		return "false"
	case isFloatIdent(ident):
		return "0.0"
	}

	typeSpec, ok := LookupTypeSpec(pass, ident)
	if !ok {
		return ""
	}

	return GetTypedZeroValue(pass, typeSpec.Type)
}

// getStructZeroValue returns the zero value for a struct type as a string representation.
// It constructs a json-like representation of the struct's zero value,
// including only the fields that are not omitted (i.e., do not have the omitempty tag).
func getStructZeroValue(pass *analysis.Pass, structType *ast.StructType) string {
	value := "{"

	jsonTagInfo, ok := pass.ResultOf[extractjsontags.Analyzer].(extractjsontags.StructFieldTags)
	if !ok {
		panic("could not get struct field tags from pass result")
	}

	for _, field := range structType.Fields.List {
		fieldTagInfo := jsonTagInfo.FieldTags(field)

		if fieldTagInfo.OmitEmpty {
			// If the field is omitted, we can use a zero value.
			// For structs, if they aren't a pointer another error will be raised.
			continue
		}

		value += fmt.Sprintf("%q: %s, ", fieldTagInfo.Name, GetTypedZeroValue(pass, field.Type))
	}

	value = strings.TrimSuffix(value, ", ")
	value += "}"

	return value
}

// GetTypedValidationHint returns a string hint for the validation that should be applied to a given type.
// This is used to suggest which markers should be applied to the field to complete the validation.
func GetTypedValidationHint(pass *analysis.Pass, expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return getIdentValidationHint(pass, t)
	case *ast.StructType:
		return "min properties/adding required fields"
	case *ast.ArrayType:
		return "min items"
	case *ast.MapType:
		return "min properties"
	case *ast.SelectorExpr:
		return getSelectorExprValidationHint(pass, t)
	default:
		return ""
	}
}

// getSelectorExprValidationHint returns a validation hint for a qualified identifier (external package type).
func getSelectorExprValidationHint(pass *analysis.Pass, selector *ast.SelectorExpr) string {
	typeOf := pass.TypesInfo.TypeOf(selector)
	if typeOf == nil {
		return ""
	}

	underlying := typeOf.Underlying()

	switch t := underlying.(type) {
	case *types.Slice:
		return "min items"
	case *types.Map:
		return "min properties"
	case *types.Struct:
		return "min properties/adding required fields"
	case *types.Basic:
		return getBasicTypeValidationHint(t)
	default:
		return ""
	}
}

// getBasicTypeValidationHint returns a validation hint for a basic type.
func getBasicTypeValidationHint(basicType *types.Basic) string {
	//nolint:exhaustive // We only handle common basic types; other types fall through to default.
	switch basicType.Kind() {
	case types.String:
		return "minimum length"
	case types.Bool:
		return ""
	case types.Int, types.Int8, types.Int16, types.Int32, types.Int64,
		types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64,
		types.Float32, types.Float64:
		return minMaxValidationHint
	default:
		return ""
	}
}

// getIdentValidationHint returns a string hint for the validation that should be applied to a given identifier.
func getIdentValidationHint(pass *analysis.Pass, ident *ast.Ident) string {
	switch {
	case isIntegerIdent(ident):
		return minMaxValidationHint
	case isStringIdent(ident):
		return "minimum length"
	case isBoolIdent(ident):
		return ""
	case isFloatIdent(ident):
		return minMaxValidationHint
	}

	typeSpec, ok := LookupTypeSpec(pass, ident)
	if !ok {
		return ""
	}

	return GetTypedValidationHint(pass, typeSpec.Type)
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
	return ident.Name == stringTypeName
}

// isBoolIdent checks if the identifier is a boolean type.
func isBoolIdent(ident *ast.Ident) bool {
	return ident.Name == "bool"
}

// isFloatIdent checks if the identifier is one of the float types.
func isFloatIdent(ident *ast.Ident) bool {
	return ident.Name == "float32" || ident.Name == "float64"
}

// IsFieldRequired checks if the field is required.
// It checks for the presence of the required marker, the kubebuilder required marker, or the k8s required marker.
func IsFieldRequired(field *ast.Field, markersAccess markershelper.Markers) bool {
	fieldMarkers := markersAccess.FieldMarkers(field)

	return fieldMarkers.Has(markers.RequiredMarker) ||
		fieldMarkers.Has(markers.KubebuilderRequiredMarker) ||
		fieldMarkers.Has(markers.K8sRequiredMarker)
}

// IsFieldOptional checks if the field is optional.
// It checks for the presence of the optional marker, the kubebuilder optional marker, or the k8s optional marker.
func IsFieldOptional(field *ast.Field, markersAccess markershelper.Markers) bool {
	fieldMarkers := markersAccess.FieldMarkers(field)

	return fieldMarkers.Has(markers.OptionalMarker) ||
		fieldMarkers.Has(markers.KubebuilderOptionalMarker) ||
		fieldMarkers.Has(markers.K8sOptionalMarker)
}
