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
package config

// LintersConfig contains configuration for individual linters.
type LintersConfig struct {
	// conditions contains configuration for the conditions linter.
	Conditions ConditionsConfig `json:"conditions"`

	// jsonTags contains configuration for the jsontags linter.
	JSONTags JSONTagsConfig `json:"jsonTags"`

	// nomaps contains configuration for the nomaps linter.
	NoMaps NoMapsConfig `json:"nomaps"`

	// optionalFields contains configuration for the optionalfields linter.
	OptionalFields OptionalFieldsConfig `json:"optionalFields"`

	// optionalOrRequired contains configuration for the optionalorrequired linter.
	OptionalOrRequired OptionalOrRequiredConfig `json:"optionalOrRequired"`

	// requiredFields contains configuration for the requiredfields linter.
	RequiredFields RequiredFieldsConfig `json:"requiredFields"`

	// statusOptional contains configuration for the statusoptional linter.
	StatusOptional StatusOptionalConfig `json:"statusOptional"`

	// uniqueMarkers contains configuration for the uniquemarkers linter.
	UniqueMarkers UniqueMarkersConfig `json:"uniqueMarkers"`
}

// ConditionsFirstField is the policy for the conditions linter.
type ConditionsFirstField string

const (
	// ConditionsFirstFieldWarn indicates that the conditions should be the first field in the struct.
	ConditionsFirstFieldWarn ConditionsFirstField = "Warn"

	// ConditionsFirstFieldIgnore indicates that the conditions do not need to be the first field in the struct.
	ConditionsFirstFieldIgnore ConditionsFirstField = "Ignore"
)

// ConditionsUseProtobuf is the policy for the conditions linter.
type ConditionsUseProtobuf string

const (
	// ConditionsUseProtobufSuggestFix indicates that the linter will emit a warning if the conditions are not using protobuf tags and suggest a fix.
	ConditionsUseProtobufSuggestFix ConditionsUseProtobuf = "SuggestFix"

	// ConditionsUseProtobufWarn indicates that the linter will emit a warning if the conditions are not using protobuf tags.
	ConditionsUseProtobufWarn ConditionsUseProtobuf = "Warn"

	// ConditionsUseProtobufIgnore indicates that the linter will not emit a warning if the conditions are not using protobuf tags.
	ConditionsUseProtobufIgnore ConditionsUseProtobuf = "Ignore"

	// ConditionsUseProtobufForbid indicates that the linter will emit an error if the conditions are using protobuf tags, a fix will also be suggested.
	ConditionsUseProtobufForbid ConditionsUseProtobuf = "Forbid"
)

// ConditionsUsePatchStrategy is the policy for the conditions linter.
type ConditionsUsePatchStrategy string

const (
	// ConditionsUsePatchStrategySuggestFix indicates that the linter will emit a warning if the conditions are not using patch strategy tags and suggest a fix.
	ConditionsUsePatchStrategySuggestFix ConditionsUsePatchStrategy = "SuggestFix"

	// ConditionsUsePatchStrategyWarn indicates that the linter will emit a warning if the conditions are not using patch strategy tags.
	ConditionsUsePatchStrategyWarn ConditionsUsePatchStrategy = "Warn"

	// ConditionsUsePatchStrategyIgnore indicates that the linter will not emit a warning if the conditions are not using patch strategy tags.
	ConditionsUsePatchStrategyIgnore ConditionsUsePatchStrategy = "Ignore"

	// ConditionsUsePatchStrategyForbid indicates that the linter will emit an error if the conditions are using patch strategy tags, a fix will also be suggested.
	ConditionsUsePatchStrategyForbid ConditionsUsePatchStrategy = "Forbid"
)

// ConditionsConfig contains configuration for the conditions linter.
type ConditionsConfig struct {
	// isFirstField indicates whether the conditions should be the first field in the struct.
	// Valid values are Warn and Ignore.
	// When set to Warn, the linter will emit a warning if the conditions are not the first field in the struct.
	// When set to Ignore, the linter will not emit a warning if the conditions are not the first field in the struct.
	// When otherwise not specified, the default value is Warn.
	IsFirstField ConditionsFirstField `json:"isFirstField"`

	// useProtobuf indicates whether the linter should use protobuf tags.
	// Valid values are SuggestFix, Warn, Ignore and Forbid.
	// When set to SuggestFix, the linter will emit a warning if the conditions are not using protobuf tags and suggest a fix.
	// When set to Warn, the linter will emit a warning if the conditions are not using protobuf tags.
	// When set to Ignore, the linter will not emit a warning if the conditions are not using protobuf tags.
	// When set to Forbid, the linter will emit an error if the conditions are using protobuf tags, a fix will also be suggested.
	// When otherwise not specified, the default value is SuggestFix.
	UseProtobuf ConditionsUseProtobuf `json:"useProtobuf"`

	// usePatchStrategy indicates whether the linter should enforce the patch strategy tags.
	// Valid values are SuggestFix, Warn, Ignore and Forbid.
	// When set to SuggestFix, the linter will emit a warning if the conditions are not using patch strategy tags and suggest a fix.
	// When set to Warn, the linter will emit a warning if the conditions are not using patch strategy tags.
	// When set to Ignore, the linter will not emit a warning if the conditions are not using patch strategy tags.
	// When set to Forbid, the linter will emit an error if the conditions are using patch strategy tags, a fix will also be suggested.
	// When otherwise not specified, the default value is SuggestFix.
	UsePatchStrategy ConditionsUsePatchStrategy `json:"usePatchStrategy"`
}

// JSONTagsConfig contains configuration for the jsontags linter.
type JSONTagsConfig struct {
	// jsonTagRegex is the regular expression used to validate that json tags are in a particular format.
	// By default, the regex used is "^[a-z][a-z0-9]*(?:[A-Z][a-z0-9]*)*$" and is used to check for
	// camel case like string.
	JSONTagRegex string `json:"jsonTagRegex"`
}

// NoMapsPolicy is the policy for the nomaps linter.
type NoMapsPolicy string

const (
	// NoMapsEnforce indicates that all declarations for maps are rejected.
	NoMapsEnforce NoMapsPolicy = "Enforce"

	// NoMapsAllowStringToStringMaps indicates that only string to string maps are allowed.
	NoMapsAllowStringToStringMaps NoMapsPolicy = "AllowStringToStringMaps"

	// NoMapsIgnore indicates that all declarations which the value type is a primitive type are allowed.
	NoMapsIgnore NoMapsPolicy = "Ignore"
)

// NoMapsConfig contains configuration for the nomaps linter.
type NoMapsConfig struct {
	// policy is the policy for the nomaps linter.
	// Valid values are "Enforce", "AllowStringToStringMaps" and "Ignore".
	// When set to "Enforce", all declarations for maps are rejected.
	// When set to "AllowStringToStringMaps", only string to string maps are allowed.
	// When set to "Ignore", maps of primitive types are allowed, but maps containing complex types are not allowed.
	// When otherwise not specified, the default value is "AllowStringToStringMaps".
	Policy NoMapsPolicy `json:"policy"`
}

// OptionalFieldsConfig is the configuration for the optionalfields linter.
type OptionalFieldsConfig struct {
	// pointers is the policy for pointers in optional fields.
	// This defines how the linter should handle optional fields, and whether they should be pointers or not.
	// By default, all fields will be expected to be pointers, and the linter will suggest fixes if they are not.
	Pointers OptionalFieldsPointers `json:"pointers"`

	// omitempty is the policy for the `omitempty` tag within the json tag for fields.
	// This defines how the linter should handle optional fields, and whether they should have the omitempty tag or not.
	// By default, all fields will be expected to have the `omitempty` tag.
	OmitEmpty OptionalFieldsOmitEmpty `json:"omitempty"`
}

// OptionalFieldsPointers is the configuration for pointers in optional fields.
type OptionalFieldsPointers struct {
	// preference determines whether the linter should prefer pointers for all optional fields,
	// or only for optional fields where validation or serialization requires a pointer.
	// Valid values are "Always" and "WhenRequired".
	// When set to "Always", the linter will prefer pointers for all optional fields.
	// When set to "WhenRequired", the linter will prefer pointers for optional fields where validation or serialization requires a pointer.
	// The "WhenRequired" option requires bounds on strings and numerical values to be able to acurately determine the correct pointer vs non-pointer decision.
	// When otherwise not specified, the default value is "Always".
	Preference OptionalFieldsPointerPreference `json:"preference"`
	// policy is the policy for the pointer preferences for optional fields.
	// Valid values are "SuggestFix" and "Warn".
	// When set to "SuggestFix", the linter will emit a warning if the pointer preference is not followed and suggest a fix.
	// When set to "Warn", the linter will emit a warning if the pointer preference is not followed.
	// When otherwise not specified, the default value is "SuggestFix".
	Policy OptionalFieldsPointerPolicy `json:"policy"`
}

// OptionalFieldsOmitEmpty is the configuration for the `omitempty` tag on optional fields.
type OptionalFieldsOmitEmpty struct {
	// policy determines whether the linter should require omitempty for all optional fields.
	// Valid values are "SuggestFix" and "Ignore".
	// When set to "SuggestFix", the linter will suggest adding the `omitempty` tag when an optional field does not have it.
	// When set to "Warn", the linter will emit a warning if the field does not have the `omitempty` tag.
	// When set to "Ignore", and optional field missing the `omitempty` tag will be ignored.
	// Note, when set to "Ignore", and a field does not have the `omitempty` tag, this may affect whether the field should be a pointer or not.
	Policy OptionalFieldsOmitEmptyPolicy `json:"policy"`
}

// OptionalFieldsPointerPreference is the preference for pointers in optional fields.
type OptionalFieldsPointerPreference string

const (
	// OptionalFieldsPointerPreferenceAlways indicates that the linter should prefer pointers for all optional fields.
	OptionalFieldsPointerPreferenceAlways OptionalFieldsPointerPreference = "Always"

	// OptionalFieldsPointerPreferenceWhenRequired indicates that the linter should prefer pointers for optional fields where validation or serialization requires a pointer.
	OptionalFieldsPointerPreferenceWhenRequired OptionalFieldsPointerPreference = "WhenRequired"
)

// OptionalFieldsPointerPolicy is the policy for pointers in optional fields.
type OptionalFieldsPointerPolicy string

const (
	// OptionalFieldsPointerPolicySuggestFix indicates that the linter will emit a warning if the pointer preference is not followed and suggest a fix.
	OptionalFieldsPointerPolicySuggestFix OptionalFieldsPointerPolicy = "SuggestFix"

	// OptionalFieldsPointerPolicyWarn indicates that the linter will emit a warning if the pointer preference is not followed.
	OptionalFieldsPointerPolicyWarn OptionalFieldsPointerPolicy = "Warn"
)

// OptionalFieldsOmitEmptyPolicy is the policy for the omitempty tag on optional fields.
type OptionalFieldsOmitEmptyPolicy string

const (
	// OptionalFieldsOmitEmptyPolicySuggestFix indicates that the linter will emit a warning if the field does not have omitempty, and suggest a fix.
	OptionalFieldsOmitEmptyPolicySuggestFix OptionalFieldsOmitEmptyPolicy = "SuggestFix"

	// OptionalFieldsOmitEmptyPolicyWarn indicates that the linter will emit a warning if the field does not have omitempty.
	OptionalFieldsOmitEmptyPolicyWarn OptionalFieldsOmitEmptyPolicy = "Warn"

	// OptionalFieldsOmitEmptyPolicyIgnore indicates that the linter will ignore any field missing the omitempty tag.
	OptionalFieldsOmitEmptyPolicyIgnore OptionalFieldsOmitEmptyPolicy = "Ignore"
)

// OptionalOrRequiredConfig contains configuration for the optionalorrequired linter.
type OptionalOrRequiredConfig struct {
	// preferredOptionalMarker is the preferred marker to use for optional fields.
	// If this field is not set, the default value is "optional".
	// Valid values are "optional" and "kubebuilder:validation:Optional".
	PreferredOptionalMarker string `json:"preferredOptionalMarker"`

	// preferredRequiredMarker is the preferred marker to use for required fields.
	// If this field is not set, the default value is "required".
	// Valid values are "required" and "kubebuilder:validation:Required".
	PreferredRequiredMarker string `json:"preferredRequiredMarker"`
}

// RequiredFieldPointerPolicy is the policy for pointers in required fields.
type RequiredFieldPointerPolicy string

const (
	// RequiredFieldPointerWarn indicates that the linter will emit a warning if a required field is a pointer.
	RequiredFieldPointerWarn RequiredFieldPointerPolicy = "Warn"

	// RequiredFieldPointerSuggestFix indicates that the linter will emit a warning if a required field is a pointer and suggest a fix.
	RequiredFieldPointerSuggestFix RequiredFieldPointerPolicy = "SuggestFix"
)

// RequiredFieldsConfig contains configuration for the requiredfields linter.
type RequiredFieldsConfig struct {
	// pointerPolicy is the policy for pointers in required fields.
	// Valid values are "Warn" and "SuggestFix".
	// When set to "Warn", the linter will emit a warning if a required field is a pointer.
	// When set to "SuggestFix", the linter will emit a warning if a required field is a pointer and suggest a fix.
	// When otherwise not specified, the default value is "SuggestFix".
	PointerPolicy RequiredFieldPointerPolicy `json:"pointerPolicy"`
}

// StatusOptionalConfig contains configuration for the statusoptional linter.
type StatusOptionalConfig struct {
	// preferredOptionalMarker is the preferred marker to use for optional fields.
	// If this field is not set, the default value is "optional".
	// Valid values are "optional", "kubebuilder:validation:Optional" and "k8s:optional".
	PreferredOptionalMarker string `json:"preferredOptionalMarker"`
}

// UniqueMarkersConfig contains the configuration for the uniquemarkers linter.
type UniqueMarkersConfig struct {
	// customMarkers is the set of custom marker/attribute combinations that
	// should not appear more than once on a type/field.
	// Entries must have unique identifiers.
	// Entries must start and end with alpha characters and must consist of only alpha characters and colons (':').
	CustomMarkers []UniqueMarker `json:"customMarkers"`
}

// UniqueMarker represents an instance of a marker that should
// be unique for a field/type. A marker consists
// of an identifier and attributes that can be used
// to dictate uniqueness.
type UniqueMarker struct {
	// identifier configures the marker identifier that should be unique.
	// Some common examples are "kubebuilder:validation:Enum" and "kubebuilder:validation:XValidation".
	Identifier string `json:"identifier"`
	// attributes configures the attributes that should be considered
	// as part of the uniqueness evaluation.
	// If an attribute in this list is not found in a marker definition,
	// it is interpreted as the empty value.
	//
	// Entries must be unique.
	Attributes []string `json:"attributes"`
}
