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
package validation

import (
	"fmt"
	"regexp"

	"sigs.k8s.io/kube-api-linter/pkg/config"
	"sigs.k8s.io/kube-api-linter/pkg/markers"

	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

// ValidateLintersConfig is used to validate the configuration in the config.LintersConfig struct.
func ValidateLintersConfig(lc config.LintersConfig, fldPath *field.Path) field.ErrorList {
	fieldErrors := field.ErrorList{}

	fieldErrors = append(fieldErrors, validateConditionsConfig(lc.Conditions, fldPath.Child("conditions"))...)
	fieldErrors = append(fieldErrors, validateJSONTagsConfig(lc.JSONTags, fldPath.Child("jsonTags"))...)
	fieldErrors = append(fieldErrors, validateNoMapsConfig(lc.NoMaps, fldPath.Child("nomaps"))...)
	fieldErrors = append(fieldErrors, validateOptionalFieldsConfig(lc.OptionalFields, fldPath.Child("optionalFields"))...)
	fieldErrors = append(fieldErrors, validateOptionalOrRequiredConfig(lc.OptionalOrRequired, fldPath.Child("optionalOrRequired"))...)
	fieldErrors = append(fieldErrors, validateRequiredFieldsConfig(lc.RequiredFields, fldPath.Child("requiredFields"))...)
	fieldErrors = append(fieldErrors, validateStatusOptionalConfig(lc.StatusOptional, fldPath.Child("statusOptional"))...)
	fieldErrors = append(fieldErrors, validateUniqueMarkersConfig(lc.UniqueMarkers, fldPath.Child("uniqueMarkers"))...)

	return fieldErrors
}

// validateConditionsConfig is used to validate the configuration in the config.ConditionsConfig struct.
func validateConditionsConfig(cc config.ConditionsConfig, fldPath *field.Path) field.ErrorList {
	fieldErrors := field.ErrorList{}

	switch cc.IsFirstField {
	case "", config.ConditionsFirstFieldWarn, config.ConditionsFirstFieldIgnore:
	default:
		fieldErrors = append(fieldErrors, field.Invalid(fldPath.Child("isFirstField"), cc.IsFirstField, fmt.Sprintf("invalid value, must be one of %q, %q or omitted", config.ConditionsFirstFieldWarn, config.ConditionsFirstFieldIgnore)))
	}

	switch cc.UseProtobuf {
	case "", config.ConditionsUseProtobufSuggestFix, config.ConditionsUseProtobufWarn, config.ConditionsUseProtobufIgnore, config.ConditionsUseProtobufForbid:
	default:
		fieldErrors = append(fieldErrors, field.Invalid(fldPath.Child("useProtobuf"), cc.UseProtobuf, fmt.Sprintf("invalid value, must be one of %q, %q, %q, %q or omitted", config.ConditionsUseProtobufSuggestFix, config.ConditionsUseProtobufWarn, config.ConditionsUseProtobufIgnore, config.ConditionsUseProtobufForbid)))
	}

	switch cc.UsePatchStrategy {
	case "", config.ConditionsUsePatchStrategySuggestFix, config.ConditionsUsePatchStrategyWarn, config.ConditionsUsePatchStrategyIgnore, config.ConditionsUsePatchStrategyForbid:
	default:
		fieldErrors = append(fieldErrors, field.Invalid(fldPath.Child("usePatchStrategy"), cc.UsePatchStrategy, fmt.Sprintf("invalid value, must be one of %q, %q, %q, %q or omitted", config.ConditionsUsePatchStrategySuggestFix, config.ConditionsUsePatchStrategyWarn, config.ConditionsUsePatchStrategyIgnore, config.ConditionsUsePatchStrategyForbid)))
	}

	return fieldErrors
}

// validateJSONTagsConfig is used to validate the configuration in the config.JSONTagsConfig struct.
func validateJSONTagsConfig(jtc config.JSONTagsConfig, fldPath *field.Path) field.ErrorList {
	fieldErrors := field.ErrorList{}

	if jtc.JSONTagRegex != "" {
		if _, err := regexp.Compile(jtc.JSONTagRegex); err != nil {
			fieldErrors = append(fieldErrors, field.Invalid(fldPath.Child("jsonTagRegex"), jtc.JSONTagRegex, fmt.Sprintf("invalid regex: %v", err)))
		}
	}

	return fieldErrors
}

// validateNoMapsConfig is used to validate the configuration in the config.NoMapsConfig struct.
func validateNoMapsConfig(nmc config.NoMapsConfig, fldPath *field.Path) field.ErrorList {
	fieldErrors := field.ErrorList{}

	switch nmc.Policy {
	case config.NoMapsEnforce, config.NoMapsAllowStringToStringMaps, config.NoMapsIgnore, "":
		// Valid values
	default:
		fieldErrors = append(fieldErrors, field.Invalid(fldPath.Child("policy"), nmc.Policy, fmt.Sprintf("invalid value, must be one of %q, %q, %q or omitted", config.NoMapsEnforce, config.NoMapsAllowStringToStringMaps, config.NoMapsIgnore)))
	}

	return fieldErrors
}

// validateOptionalFieldsConfig is used to validate the configuration in the config.OptionalFieldsConfig struct.
func validateOptionalFieldsConfig(ofc config.OptionalFieldsConfig, fldPath *field.Path) field.ErrorList {
	fieldErrors := field.ErrorList{}

	fieldErrors = append(fieldErrors, validateOptionFieldsPointers(ofc.Pointers, fldPath.Child("pointers"))...)
	fieldErrors = append(fieldErrors, validateOptionFieldsOmitEmpty(ofc.OmitEmpty, fldPath.Child("omitEmpty"))...)

	return fieldErrors
}

// validateOptionFieldsPointers is used to validate the configuration in the config.OptionalFieldsPointers struct.
func validateOptionFieldsPointers(opc config.OptionalFieldsPointers, fldPath *field.Path) field.ErrorList {
	fieldErrors := field.ErrorList{}

	switch opc.Preference {
	case "", config.OptionalFieldsPointerPreferenceAlways, config.OptionalFieldsPointerPreferenceWhenRequired:
	default:
		fieldErrors = append(fieldErrors, field.Invalid(fldPath.Child("preference"), opc.Preference, fmt.Sprintf("invalid value, must be one of %q, %q or omitted", config.OptionalFieldsPointerPreferenceAlways, config.OptionalFieldsPointerPreferenceWhenRequired)))
	}

	switch opc.Policy {
	case "", config.OptionalFieldsPointerPolicySuggestFix, config.OptionalFieldsPointerPolicyWarn:
	default:
		fieldErrors = append(fieldErrors, field.Invalid(fldPath.Child("policy"), opc.Policy, fmt.Sprintf("invalid value, must be one of %q, %q or omitted", config.OptionalFieldsPointerPolicySuggestFix, config.OptionalFieldsPointerPolicyWarn)))
	}

	return fieldErrors
}

// validateOptionFieldsOmitEmpty is used to validate the configuration in the config.OptionalFieldsOmitEmpty struct.
func validateOptionFieldsOmitEmpty(oec config.OptionalFieldsOmitEmpty, fldPath *field.Path) field.ErrorList {
	fieldErrors := field.ErrorList{}

	switch oec.Policy {
	case "", config.OptionalFieldsOmitEmptyPolicyIgnore, config.OptionalFieldsOmitEmptyPolicyWarn, config.OptionalFieldsOmitEmptyPolicySuggestFix:
	default:
		fieldErrors = append(fieldErrors, field.Invalid(fldPath.Child("policy"), oec.Policy, fmt.Sprintf("invalid value, must be one of %q, %q, %q or omitted", config.OptionalFieldsOmitEmptyPolicyIgnore, config.OptionalFieldsOmitEmptyPolicyWarn, config.OptionalFieldsOmitEmptyPolicySuggestFix)))
	}

	return fieldErrors
}

// validateOptionalOrRequiredConfig is used to validate the configuration in the config.OptionalOrRequiredConfig struct.
func validateOptionalOrRequiredConfig(oorc config.OptionalOrRequiredConfig, fldPath *field.Path) field.ErrorList {
	fieldErrors := field.ErrorList{}

	switch oorc.PreferredOptionalMarker {
	case "", markers.OptionalMarker, markers.KubebuilderOptionalMarker:
	default:
		fieldErrors = append(fieldErrors, field.Invalid(fldPath.Child("preferredOptionalMarker"), oorc.PreferredOptionalMarker, fmt.Sprintf("invalid value, must be one of %q, %q or omitted", markers.OptionalMarker, markers.KubebuilderOptionalMarker)))
	}

	switch oorc.PreferredRequiredMarker {
	case "", markers.RequiredMarker, markers.KubebuilderRequiredMarker:
	default:
		fieldErrors = append(fieldErrors, field.Invalid(fldPath.Child("preferredRequiredMarker"), oorc.PreferredRequiredMarker, fmt.Sprintf("invalid value, must be one of %q, %q or omitted", markers.RequiredMarker, markers.KubebuilderRequiredMarker)))
	}

	return fieldErrors
}

// validateStatusOptionalConfig is used to validate the configuration in the config.StatusOptionalConfig struct.
func validateStatusOptionalConfig(soc config.StatusOptionalConfig, fldPath *field.Path) field.ErrorList {
	fieldErrors := field.ErrorList{}

	switch soc.PreferredOptionalMarker {
	case "", markers.OptionalMarker, markers.KubebuilderOptionalMarker, markers.K8sOptionalMarker:
	default:
		fieldErrors = append(fieldErrors, field.Invalid(fldPath.Child("preferredOptionalMarker"), soc.PreferredOptionalMarker, fmt.Sprintf("invalid value, must be one of %q, %q, %q or omitted", markers.OptionalMarker, markers.KubebuilderOptionalMarker, markers.K8sOptionalMarker)))
	}

	return fieldErrors
}

// validateRequiredFieldsConfig is used to validate the configuration in the config.RequiredFieldsConfig struct.
func validateRequiredFieldsConfig(rfc config.RequiredFieldsConfig, fldPath *field.Path) field.ErrorList {
	fieldErrors := field.ErrorList{}

	switch rfc.PointerPolicy {
	case "", config.RequiredFieldPointerWarn, config.RequiredFieldPointerSuggestFix:
	default:
		fieldErrors = append(fieldErrors, field.Invalid(fldPath.Child("pointerPolicy"), rfc.PointerPolicy, fmt.Sprintf("invalid value, must be one of %q, %q or omitted", config.RequiredFieldPointerWarn, config.RequiredFieldPointerSuggestFix)))
	}

	return fieldErrors
}

func validateUniqueMarkersConfig(umc config.UniqueMarkersConfig, fldPath *field.Path) field.ErrorList {
	fieldErrors := field.ErrorList{}
	identifierSet := sets.New[string]()

	for i, marker := range umc.CustomMarkers {
		if identifierSet.Has(marker.Identifier) {
			fieldErrors = append(fieldErrors, field.Invalid(fldPath.Child("customMarkers").Index(i).Child("identifier"), marker.Identifier, "repeated value, values must be unique"))
			continue
		}

		fieldErrors = append(fieldErrors, validateUniqueMarker(marker, fldPath.Child("customMarkers").Index(i))...)

		identifierSet.Insert(marker.Identifier)
	}

	return fieldErrors
}

func validateUniqueMarker(um config.UniqueMarker, fldPath *field.Path) field.ErrorList {
	fieldErrors := field.ErrorList{}
	attrSet := sets.New[string]()

	for i, attr := range um.Attributes {
		if attrSet.Has(attr) {
			fieldErrors = append(fieldErrors, field.Invalid(fldPath.Child("attributes").Index(i), attr, "repeated value, values must be unique"))
		}
	}

	return fieldErrors
}
