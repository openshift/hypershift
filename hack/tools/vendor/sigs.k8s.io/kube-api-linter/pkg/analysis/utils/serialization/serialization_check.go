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
package serialization

import (
	"fmt"
	"go/ast"

	"golang.org/x/tools/go/analysis"
	"sigs.k8s.io/kube-api-linter/pkg/analysis/helpers/extractjsontags"
	markershelper "sigs.k8s.io/kube-api-linter/pkg/analysis/helpers/markers"
	"sigs.k8s.io/kube-api-linter/pkg/analysis/utils"
)

// SerializationCheck is an interface for checking serialization of fields.
type SerializationCheck interface {
	Check(pass *analysis.Pass, field *ast.Field, markersAccess markershelper.Markers, jsonTags extractjsontags.FieldTagInfo)
}

// New creates a new SerializationCheck with the given configuration.
func New(cfg *Config) SerializationCheck {
	validateConfig(cfg)

	return &serializationCheck{
		pointerPolicy:     cfg.Pointers.Policy,
		pointerPreference: cfg.Pointers.Preference,
		omitEmptyPolicy:   cfg.OmitEmpty.Policy,
		omitZeroPolicy:    cfg.OmitZero.Policy,
	}
}

// validateConfig validates the configuration.
// We panic if the configuration is invalid as this checker is intended to be
// used as an implementation detail of the kube-api-linter.
// Linters implementing this checker should validate the configuration themselves.
func validateConfig(cfg *Config) {
	if cfg == nil {
		panic("configuration must be provided")
	}

	switch cfg.Pointers.Policy {
	case PointersPolicySuggestFix, PointersPolicyWarn:
	default:
		panic(fmt.Sprintf("pointers.policy is required and must be one of %q or %q", PointersPolicySuggestFix, PointersPolicyWarn))
	}

	switch cfg.Pointers.Preference {
	case PointersPreferenceAlways, PointersPreferenceWhenRequired:
	default:
		panic(fmt.Sprintf("pointers.preference is required and must be one of %q or %q", PointersPreferenceAlways, PointersPreferenceWhenRequired))
	}

	switch cfg.OmitEmpty.Policy {
	case OmitEmptyPolicySuggestFix, OmitEmptyPolicyWarn, OmitEmptyPolicyIgnore:
	default:
		panic(fmt.Sprintf("omitempty.policy is required and must be one of %q, %q or %q", OmitEmptyPolicySuggestFix, OmitEmptyPolicyWarn, OmitEmptyPolicyIgnore))
	}

	switch cfg.OmitZero.Policy {
	case OmitZeroPolicySuggestFix, OmitZeroPolicyWarn, OmitZeroPolicyForbid:
	default:
		panic(fmt.Sprintf("omitzero.policy is required and must be one of %q, %q or %q", OmitZeroPolicySuggestFix, OmitZeroPolicyWarn, OmitZeroPolicyForbid))
	}
}

// serializationCheck is the implementation of the SerializationCheck interface.
type serializationCheck struct {
	pointerPolicy     PointersPolicy
	pointerPreference PointersPreference
	omitEmptyPolicy   OmitEmptyPolicy
	omitZeroPolicy    OmitZeroPolicy
}

// Check checks the serialization of the field.
// It will check if the zero value of the field is valid, and whether the field should be a pointer or not.
func (s *serializationCheck) Check(pass *analysis.Pass, field *ast.Field, markersAccess markershelper.Markers, jsonTags extractjsontags.FieldTagInfo) {
	fieldName := utils.FieldName(field)

	hasValidZeroValue, completeValidation := utils.IsZeroValueValid(pass, field, field.Type, markersAccess, s.omitZeroPolicy != OmitZeroPolicyForbid)
	hasOmitEmpty := jsonTags.OmitEmpty
	hasOmitZero := jsonTags.OmitZero
	isPointer, underlying := utils.IsStarExpr(field.Type)
	isStruct := utils.IsStructType(pass, field.Type)

	switch s.pointerPreference {
	case PointersPreferenceAlways:
		// The field must always be a pointer, pointers require omitempty, so enforce that too.
		s.handleFieldShouldBePointer(pass, field, fieldName, isPointer, underlying, "should be a pointer.")
		s.handleFieldShouldHaveOmitEmpty(pass, field, fieldName, hasOmitEmpty, jsonTags)
	case PointersPreferenceWhenRequired:
		s.handleFieldOmitZero(pass, field, fieldName, jsonTags, hasOmitZero, hasValidZeroValue, isPointer, isStruct)

		if s.omitEmptyPolicy != OmitEmptyPolicyIgnore || hasOmitEmpty {
			// If we require omitempty, or the field has omitempty, we can check the field properties based on it being an omitempty field.
			s.checkFieldPropertiesWithOmitEmptyRequired(pass, field, fieldName, jsonTags, underlying, hasOmitEmpty, hasValidZeroValue, completeValidation, isPointer, isStruct)
		} else {
			// The field does not have omitempty, and does not require it.
			s.checkFieldPropertiesWithoutOmitEmpty(pass, field, fieldName, jsonTags, underlying, hasValidZeroValue, completeValidation, isPointer, isStruct)
		}
	default:
		panic(fmt.Sprintf("unknown pointer preference: %s", s.pointerPreference))
	}
}

func (s *serializationCheck) handleFieldOmitZero(pass *analysis.Pass, field *ast.Field, fieldName string, jsonTags extractjsontags.FieldTagInfo, hasOmitZero, hasValidZeroValue, isPointer, isStruct bool) {
	switch s.omitZeroPolicy {
	case OmitZeroPolicyForbid:
		// when the omitzero policy is set to forbid, we need to report removing omitzero if set on the struct fields.
		s.checkFieldPropertiesWithOmitZeroForbidPolicy(pass, field, fieldName, isStruct, hasOmitZero, jsonTags)
	case OmitZeroPolicyWarn, OmitZeroPolicySuggestFix:
		// If we require omitzero, or the field has omitzero, we can check the field properties based on it being an omitzero field.
		s.checkFieldPropertiesWithOmitZeroRequired(pass, field, fieldName, jsonTags, hasOmitZero, isPointer, isStruct, hasValidZeroValue)
	default:
		panic(fmt.Sprintf("unknown omit zero policy: %s", s.omitZeroPolicy))
	}
}

func (s *serializationCheck) handleFieldShouldHaveOmitEmpty(pass *analysis.Pass, field *ast.Field, fieldName string, hasOmitEmpty bool, jsonTags extractjsontags.FieldTagInfo) {
	if hasOmitEmpty {
		return
	}

	reportShouldAddOmitEmpty(pass, field, s.omitEmptyPolicy, fieldName, "field %s should have the omitempty tag.", jsonTags)
}

func (s *serializationCheck) checkFieldPropertiesWithOmitEmptyRequired(pass *analysis.Pass, field *ast.Field, fieldName string, jsonTags extractjsontags.FieldTagInfo, underlying ast.Expr, hasOmitEmpty, hasValidZeroValue, completeValidation, isPointer, isStruct bool) {
	switch {
	case isStruct && !hasValidZeroValue && s.omitZeroPolicy != OmitZeroPolicyForbid:
		// The struct field need not be pointer if it does not have a valid zero value.
		return
	case hasValidZeroValue && !completeValidation:
		zeroValue := utils.GetTypedZeroValue(pass, underlying)
		validationHint := utils.GetTypedValidationHint(pass, underlying)

		s.handleFieldShouldBePointer(pass, field, fieldName, isPointer, underlying, fmt.Sprintf("has a valid zero value (%s), but the validation is not complete (e.g. %s). The field should be a pointer to allow the zero value to be set. If the zero value is not a valid use case, complete the validation and remove the pointer.", zeroValue, validationHint))
	case hasValidZeroValue, isStruct:
		// The field validation infers that the zero value is valid, the field needs to be a pointer.
		// Structs with omitempty should always be pointers, else they won't actually be omitted.
		zeroValue := utils.GetTypedZeroValue(pass, underlying)

		s.handleFieldShouldBePointer(pass, field, fieldName, isPointer, underlying, fmt.Sprintf("has a valid zero value (%s) and should be a pointer.", zeroValue))
	case !hasValidZeroValue && completeValidation && !isStruct:
		// The validation is fully complete, and the zero value is not valid, so we don't need a pointer.
		s.handleFieldShouldNotBePointer(pass, field, fieldName, isPointer, "field %s does not allow the zero value. The field does not need to be a pointer.")
	}

	// In this case, we should always add the omitempty if it isn't present.
	s.handleFieldShouldHaveOmitEmpty(pass, field, fieldName, hasOmitEmpty, jsonTags)
}

func (s *serializationCheck) checkFieldPropertiesWithoutOmitEmpty(pass *analysis.Pass, field *ast.Field, fieldName string, jsonTags extractjsontags.FieldTagInfo, underlying ast.Expr, hasValidZeroValue, completeValidation, isPointer, isStruct bool) {
	switch {
	case hasValidZeroValue:
		// The field is not omitempty, and the zero value is valid, the field does not need to be a pointer.
		s.handleFieldShouldNotBePointer(pass, field, fieldName, isPointer, "field %s does not have omitempty and allows the zero value. The field does not need to be a pointer.")
	case !hasValidZeroValue:
		// The zero value would not be accepted, so the field needs to have omitempty.
		// Force the omitempty policy to suggest a fix. We can only get to this function when the policy is configured to Ignore.
		// Since we absolutely have to add the omitempty tag, we can report it as a suggestion.
		reportShouldAddOmitEmpty(pass, field, OmitEmptyPolicySuggestFix, fieldName, "field %s does not allow the zero value. It must have the omitempty tag.", jsonTags)
		// Once it has the omitempty tag, it will also need to be a pointer in some cases.
		// Now handle it as if it had the omitempty already.
		// We already handle the omitempty tag above, so force the `hasOmitEmpty` to true.
		s.checkFieldPropertiesWithOmitEmptyRequired(pass, field, fieldName, jsonTags, underlying, true, hasValidZeroValue, completeValidation, isPointer, isStruct)
	}
}

func (s *serializationCheck) checkFieldPropertiesWithOmitZeroRequired(pass *analysis.Pass, field *ast.Field, fieldName string, jsonTags extractjsontags.FieldTagInfo, hasOmitZero, isPointer, isStruct, hasValidZeroValue bool) {
	if !isStruct || hasValidZeroValue {
		return
	}

	s.handleFieldShouldHaveOmitZero(pass, field, fieldName, hasOmitZero, jsonTags)
	s.handleFieldShouldNotBePointer(pass, field, fieldName, isPointer, "field %s does not allow the zero value. The field does not need to be a pointer.")
}

func (s *serializationCheck) checkFieldPropertiesWithOmitZeroForbidPolicy(pass *analysis.Pass, field *ast.Field, fieldName string, isStruct, hasOmitZero bool, jsonTags extractjsontags.FieldTagInfo) {
	if !isStruct || !hasOmitZero {
		// Handle omitzero only for struct field having omitZero tag.
		return
	}

	reportShouldRemoveOmitZero(pass, field, fieldName, jsonTags)
}

func (s *serializationCheck) handleFieldShouldHaveOmitZero(pass *analysis.Pass, field *ast.Field, fieldName string, hasOmitZero bool, jsonTags extractjsontags.FieldTagInfo) {
	if hasOmitZero {
		return
	}

	// Currently, add omitzero tags to only struct fields.
	reportShouldAddOmitZero(pass, field, s.omitZeroPolicy, fieldName, "field %s does not allow the zero value. It must have the omitzero tag.", jsonTags)
}

func (s *serializationCheck) handleFieldShouldBePointer(pass *analysis.Pass, field *ast.Field, fieldName string, isPointer bool, underlying ast.Expr, reason string) {
	if utils.IsPointerType(pass, underlying) {
		if isPointer {
			switch s.pointerPolicy {
			case PointersPolicySuggestFix:
				reportShouldRemovePointer(pass, field, PointersPolicySuggestFix, fieldName, "field %s underlying type does not need to be a pointer. The pointer should be removed.", fieldName)
			case PointersPolicyWarn:
				pass.Reportf(field.Pos(), "field %s underlying type does not need to be a pointer. The pointer should be removed.", fieldName)
			}
		}

		return
	}

	if isPointer {
		return
	}

	switch s.pointerPolicy {
	case PointersPolicySuggestFix:
		reportShouldAddPointer(pass, field, PointersPolicySuggestFix, fieldName, "field %s %s", fieldName, reason)
	case PointersPolicyWarn:
		pass.Reportf(field.Pos(), "field %s %s", fieldName, reason)
	}
}

func (s *serializationCheck) handleFieldShouldNotBePointer(pass *analysis.Pass, field *ast.Field, fieldName string, isPointer bool, message string) {
	if !isPointer {
		return
	}

	reportShouldRemovePointer(pass, field, s.pointerPolicy, fieldName, message, fieldName)
}
