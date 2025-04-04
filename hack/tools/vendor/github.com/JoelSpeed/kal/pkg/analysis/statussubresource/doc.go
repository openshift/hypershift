// statussubresource is a linter to check that the status subresource is configured correctly for
// structs marked with the 'kubebuilder:object:root:=true' marker. Correct configuration is that
// when there is a status field the 'kubebuilder:subresource:status' marker is present on the struct
// OR when the 'kubebuilder:subresource:status' marker is present on the struct there is a status field.
//
// In the case where there is a status field present but no 'kubebuilder:subresource:status' marker, the
// linter will suggest adding the comment '// +kubebuilder:subresource:status' above the struct.
//
// This linter is not enabled by default as it is only applicable to CustomResourceDefinitions.
package statussubresource
