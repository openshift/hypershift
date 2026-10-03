package v1beta1

// LabelValue is a Kubernetes label value. Values can be empty or up to 63 characters long,
// consisting of alphanumeric characters, dashes (-), underscores (_), or dots (.),
// and must begin and end with an alphanumeric character when non-empty.
// This follows the Kubernetes label syntax and character set documented at
// https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/#syntax-and-character-set.
// It is used as the map value type so the generated CRD can validate values under
// additionalProperties. This intentionally changes the Go source type of the
// HostedClusterSpec.Labels and HostedControlPlaneSpec.Labels fields from
// map[string]string to map[string]LabelValue; the JSON representation is unchanged.
// Keeping map[string]string would preserve source compatibility, but would leave
// additionalProperties unable to enforce Kubernetes label-value validation in the CRD.
// Existing Go callers must migrate by allocating map[string]LabelValue and copying each
// value as LabelValue(value); generated apply-configuration WithLabels methods use the
// same typed map. This is a deliberate v1beta1 source-compatibility break for complete
// admission validation, not a claim that wire compatibility implies source compatibility.
//
// The validation rule mirrors Kubernetes validation.IsValidLabelValue so values
// propagated to Pod labels use the same character set and length limit.
// The 63-character maximum is the Kubernetes label-value limit; the 317-character
// maximum applies only to a qualified label key (253-character prefix + slash + 63-character name).
// +kubebuilder:validation:MaxLength=63
// +kubebuilder:validation:MinLength=0
// +kubebuilder:validation:XValidation:rule=`self == "" || self.matches('^[a-zA-Z0-9]([a-zA-Z0-9._-]*[a-zA-Z0-9])?$')`,message="label value must be empty or consist of alphanumeric characters, dashes (-), underscores (_) or dots (.), and must begin and end with an alphanumeric character"
type LabelValue string
