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
package ssatags

// SSATagsConfig contains configuration for the ssatags linter.
type SSATagsConfig struct {
	// listTypeSetUsage is the policy for listType=set on slices of structs,
	// slices, and maps. Valid values are "Warn" and "Ignore".
	// Defaults to "Warn". Scalar element lists are never flagged.
	ListTypeSetUsage SSATagsListTypeSetUsage `json:"listTypeSetUsage"`
}

// SSATagsListTypeSetUsage is the policy for the listType=set usage in the ssatags linter.
type SSATagsListTypeSetUsage string

const (
	// SSATagsListTypeSetUsageWarn warns when listType=set is used on slices of structs, slices, and maps.
	SSATagsListTypeSetUsageWarn SSATagsListTypeSetUsage = "Warn"

	// SSATagsListTypeSetUsageIgnore disables the check for listType=set used on slices of structs, slices, and maps.
	SSATagsListTypeSetUsageIgnore SSATagsListTypeSetUsage = "Ignore"
)
