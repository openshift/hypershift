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
package requiredfields

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
