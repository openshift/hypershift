package v1beta1

// GCPPlatformSpec specifies configuration for clusters running on Google Cloud Platform.
type GCPPlatformSpec struct {
	// project is the GCP project ID.
	// A valid project ID must satisfy the following rules:
	//   length: Must be between 6 and 30 characters, inclusive
	//   characters: Only lowercase letters (`a-z`), digits (`0-9`), and hyphens (`-`) are allowed
	//   start and end: Must begin with a lowercase letter and must not end with a hyphen
	//   valid examples: "my-project", "my-project-1", "my-project-123".
	//
	// +required
	// +immutable
	// +kubebuilder:validation:MaxLength=30
	// +kubebuilder:validation:Pattern=`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Project is immutable"
	Project string `json:"project"`

	// region is the GCP region in which the cluster resides.
	// A valid region must satisfy the following rules:
	//   format: Must be in the form `<letters>-<lettersOrDigits><digit>`
	//   characters: Only lowercase letters (`a-z`), digits (`0-9`), and a single hyphen (`-`) separator
	//   valid examples: "us-central1", "europe-west2"
	//   region must not include zone suffixes (e.g., "-a").
	// For a full list of valid regions, see: https://cloud.google.com/compute/docs/regions-zones.
	//
	// +required
	// +immutable
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z]+-[a-z0-9]+[0-9]$`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Region is immutable"
	Region string `json:"region"`
}
