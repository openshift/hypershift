package v1beta1

// GCPResourceReference represents a reference to a GCP resource by name.
// Follows GCP naming patterns (name-based APIs, not ID-based like AWS).
// See https://google.aip.dev/122 for GCP resource name standards.
type GCPResourceReference struct {
	// name is the name of the GCP resource
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z]([a-z0-9]*(-[a-z0-9]+)*)?$`
	Name string `json:"name"`
}

// GCPEndpointAccessType defines the endpoint access type for GCP clusters.
// Equivalent to AWS EndpointAccessType but adapted for GCP networking model.
type GCPEndpointAccessType string

const (
	// GCPEndpointAccessPublicAndPrivate endpoint access allows public API server access and
	// private node communication with the control plane via PSC.
	GCPEndpointAccessPublicAndPrivate GCPEndpointAccessType = "PublicAndPrivate"

	// GCPEndpointAccessPrivate endpoint access allows only private API server access and private
	// node communication with the control plane via PSC.
	GCPEndpointAccessPrivate GCPEndpointAccessType = "Private"
)

// GCPNetworkConfigCustomer specifies customer VPC configuration for GCP clusters.
// Customer-focused configuration for PSC endpoint creation.
type GCPNetworkConfigCustomer struct {
	// project is the customer's GCP project ID.
	// +required
	// +kubebuilder:validation:MaxLength=30
	// +kubebuilder:validation:Pattern=`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`
	Project string `json:"project"`

	// network is the customer's VPC network name
	// +required
	Network GCPResourceReference `json:"network"`

	// pscSubnet is the customer's subnet for PSC endpoints
	// +required
	PSCSubnet GCPResourceReference `json:"pscSubnet"`
}

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

	// customerNetworkConfig specifies customer VPC configuration for PSC.
	// Required for customer VPC configuration in PSC deployments.
	// +required
	CustomerNetworkConfig GCPNetworkConfigCustomer `json:"customerNetworkConfig"`

	// endpointAccess controls cluster endpoint accessibility.
	// Defaults to "Private".
	// +kubebuilder:validation:Enum=PublicAndPrivate;Private
	// +kubebuilder:default=Private
	// +optional
	EndpointAccess GCPEndpointAccessType `json:"endpointAccess,omitempty"`
}
