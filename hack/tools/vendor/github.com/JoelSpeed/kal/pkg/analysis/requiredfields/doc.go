/*
requiredFields is a linter to check that fields that are marked as required are not pointers, and do not have the omitempty tag.
The linter will check for fields that are marked as required using the +required marker, or the +kubebuilder:validation:Required marker.

The linter will suggest to remove the omitempty tag from fields that are marked as required, but have the omitempty tag.
The linter will suggest to remove the pointer type from fields that are marked as required.

If you have a large, existing codebase, you may not want to automatically fix all of the pointer issues.
In this case, you can configure the linter not to suggest fixing the pointer issues by setting the `pointerPolicy` option to `Warn`.
*/
package requiredfields
