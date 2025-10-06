package v1beta1

// GCPPlatformSpec specifies configuration for clusters running on Google Cloud Platform.
type GCPPlatformSpec struct {
	// project is the GCP project ID.
	// +required
	// +kubebuilder:validation:MaxLength=30
	// +kubebuilder:validation:Pattern=`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`
	Project string `json:"project"`

	// region is the GCP region in which the cluster resides.
	// +required
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z]+-[a-z0-9]+[0-9]$`
	Region string `json:"region"`

	// resourceTags are additional tags to apply to GCP resources created for the cluster.
	// GCP supports a maximum of 50 tags per resource.
	// +optional
	// +kubebuilder:validation:MaxItems=50
	// +kubebuilder:validation:items:MaxLength=255
	ResourceTags []string `json:"resourceTags,omitempty"`
}
