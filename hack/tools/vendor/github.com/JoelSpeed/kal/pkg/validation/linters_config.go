package validation

import (
	"fmt"
	"regexp"

	"github.com/JoelSpeed/kal/pkg/analysis/optionalorrequired"
	"github.com/JoelSpeed/kal/pkg/config"

	"k8s.io/apimachinery/pkg/util/validation/field"
)

// ValidateLintersConfig is used to validate the configuration in the config.LintersConfig struct.
func ValidateLintersConfig(lc config.LintersConfig, fldPath *field.Path) field.ErrorList {
	fieldErrors := field.ErrorList{}

	fieldErrors = append(fieldErrors, validateConditionsConfig(lc.Conditions, fldPath.Child("conditions"))...)
	fieldErrors = append(fieldErrors, validateJSONTagsConfig(lc.JSONTags, fldPath.Child("jsonTags"))...)
	fieldErrors = append(fieldErrors, validateOptionalOrRequiredConfig(lc.OptionalOrRequired, fldPath.Child("optionalOrRequired"))...)
	fieldErrors = append(fieldErrors, validateRequiredFieldsConfig(lc.RequiredFields, fldPath.Child("requiredFields"))...)

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

// validateOptionalOrRequiredConfig is used to validate the configuration in the config.OptionalOrRequiredConfig struct.
func validateOptionalOrRequiredConfig(oorc config.OptionalOrRequiredConfig, fldPath *field.Path) field.ErrorList {
	fieldErrors := field.ErrorList{}

	switch oorc.PreferredOptionalMarker {
	case "", optionalorrequired.OptionalMarker, optionalorrequired.KubebuilderOptionalMarker:
	default:
		fieldErrors = append(fieldErrors, field.Invalid(fldPath.Child("preferredOptionalMarker"), oorc.PreferredOptionalMarker, fmt.Sprintf("invalid value, must be one of %q, %q or omitted", optionalorrequired.OptionalMarker, optionalorrequired.KubebuilderOptionalMarker)))
	}

	switch oorc.PreferredRequiredMarker {
	case "", optionalorrequired.RequiredMarker, optionalorrequired.KubebuilderRequiredMarker:
	default:
		fieldErrors = append(fieldErrors, field.Invalid(fldPath.Child("preferredRequiredMarker"), oorc.PreferredRequiredMarker, fmt.Sprintf("invalid value, must be one of %q, %q or omitted", optionalorrequired.RequiredMarker, optionalorrequired.KubebuilderRequiredMarker)))
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
