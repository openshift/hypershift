/*
optionalorrequired is a linter to ensure that all fields are marked as either optional or required.
By default, it searches for the `+optional` and `+required` markers, and ensures that all fields are marked
with at least one of these markers.

The linter can be configured to use different markers, by setting the `PreferredOptionalMarker` and `PreferredRequiredMarker`.
The default values are `+optional` and `+required`, respectively.
The available alternate values for each marker are:

For PreferredOptionalMarker:
  - `+optional`: The standard Kubernetes marker for optional fields.
  - `+kubebuilder:validation:Optional`: The Kubebuilder marker for optional fields.

For PreferredRequiredMarker:
  - `+required`: The standard Kubernetes marker for required fields.
  - `+kubebuilder:validation:Required`: The Kubebuilder marker for required fields.

When a field is marked with both the Kubernetes and Kubebuilder markers, the linter will suggest to remove the Kubebuilder marker.
When a field is marked only with the Kubebuilder marker, the linter will suggest to use the Kubernetes marker instead.
This behaviour is reversed when the `PreferredOptionalMarker` and `PreferredRequiredMarker` are set to the Kubebuilder markers.

Use the linter fix option to automatically apply suggested fixes.
*/
package optionalorrequired
