/*
conditions is a linter that verifies that the conditions field within the struct is correctly defined.

conditions fields in Kuberenetes API types are expected to be a slice of metav1.Condition.
This linter verifies that the field is a slice of metav1.Condition and that it is correctly annotated with the required markers,
and tags.

The expected condition field should look like this:

	// +listType=map
	// +listMapKey=type
	// +patchStrategy=merge
	// +patchMergeKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`

Where the tags and markers are incorrect, the linter will suggest fixes to improve the field definition.

Conditions are also idiomatically the first item in the struct, the linter will highlight when the conditions field is not the first field in the struct.
If this is not a desired behaviour, set the linter config option `isFirstField` to `Ignore`.

Protobuf tags and patch strategy are required for in-tree API types, but not for CRDs.
When linting CRD based types, set the `useProtobuf` and `usePatchStrategy` config option to `Ignore` or `Forbid`.
*/
package conditions
