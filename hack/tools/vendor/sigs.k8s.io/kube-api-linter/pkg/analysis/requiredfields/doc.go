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

/*
requiredFields is a linter to check that fields that are marked as required are not pointers, and do not have the omitempty tag.
The linter will check for fields that are marked as required using the +required marker, or the +kubebuilder:validation:Required marker.

The linter will suggest to remove the omitempty tag from fields that are marked as required, but have the omitempty tag.
The linter will suggest to remove the pointer type from fields that are marked as required.

If you have a large, existing codebase, you may not want to automatically fix all of the pointer issues.
In this case, you can configure the linter not to suggest fixing the pointer issues by setting the `pointerPolicy` option to `Warn`.
*/
package requiredfields
