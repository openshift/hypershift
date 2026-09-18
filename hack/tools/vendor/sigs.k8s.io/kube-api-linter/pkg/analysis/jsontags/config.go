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
package jsontags

// FieldNameMatchPolicy controls whether json tag names must match the camelCase field name.
type FieldNameMatchPolicy string

const (
	// FieldNameMatchPolicySuggestFix emits diagnostics and suggests fixes when a json
	// tag name does not match the expected camelCase version of the Go field name.
	FieldNameMatchPolicySuggestFix FieldNameMatchPolicy = "SuggestFix"

	// FieldNameMatchPolicyWarn emits diagnostics when a json tag name does not match
	// the expected camelCase version of the Go field name.
	FieldNameMatchPolicyWarn FieldNameMatchPolicy = "Warn"

	// FieldNameMatchPolicyIgnore disables the field-name matching check.
	FieldNameMatchPolicyIgnore FieldNameMatchPolicy = "Ignore"
)

// JSONTagsConfig contains configuration for the jsontags linter.
type JSONTagsConfig struct {
	// jsonTagRegex is the regular expression used to validate that json tags are in a particular format.
	// By default, the regex used is "^[a-z][a-z0-9]*(?:[A-Z][a-z0-9]*)*$" and is used to check for
	// camel case like string.
	JSONTagRegex string `json:"jsonTagRegex"`

	// fieldNameMatch controls whether json tag names must match the camelCase
	// version of their Go field name.
	// The check uses word-splitting heuristics to preserve common acronyms and initialisms.
	// Valid values are "SuggestFix", "Warn" and "Ignore".
	// When set to "SuggestFix", diagnostics are emitted for mismatches and a fix is suggested.
	// When set to "Warn", diagnostics are emitted for mismatches.
	// When set to "Ignore", this check is disabled.
	// When otherwise not specified, the default value is "Ignore".
	FieldNameMatch FieldNameMatchPolicy `json:"fieldNameMatch"`
}
