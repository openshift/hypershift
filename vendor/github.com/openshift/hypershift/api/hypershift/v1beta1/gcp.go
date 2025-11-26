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
	// private node communication with the control plane via Private Service Connect.
	GCPEndpointAccessPublicAndPrivate GCPEndpointAccessType = "PublicAndPrivate"

	// GCPEndpointAccessPrivate endpoint access allows only private API server access and private
	// node communication with the control plane via Private Service Connect.
	GCPEndpointAccessPrivate GCPEndpointAccessType = "Private"
)

// GCPNetworkConfig specifies VPC configuration for GCP clusters and Private Service Connect endpoint creation.
type GCPNetworkConfig struct {
	// network is the VPC network name
	// +required
	Network GCPResourceReference `json:"network"`

	// privateServiceConnectSubnet is the subnet for Private Service Connect endpoints
	// +required
	PrivateServiceConnectSubnet GCPResourceReference `json:"privateServiceConnectSubnet"`
}

// GCPPlatformSpec specifies configuration for clusters running on Google Cloud Platform.
type GCPPlatformSpec struct {
	// project is the GCP project ID.
	// A valid project ID must satisfy the following rules:
	//   length: Must be between 6 and 30 characters, inclusive
	//   characters: Only lowercase letters (`a-z`), digits (`0-9`), and hyphens (`-`) are allowed
	//   start and end: Must begin with a lowercase letter and must not end with a hyphen
	//   hyphens: No consecutive hyphens are allowed (e.g., "my--project" is invalid)
	//   valid examples: "my-project", "my-project-1", "my-project-123".
	//
	// +required
	// +immutable
	// +kubebuilder:validation:MinLength=6
	// +kubebuilder:validation:MaxLength=30
	// +kubebuilder:validation:Pattern=`^[a-z]([a-z0-9]*(-[a-z0-9]+)*)?$`
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

	// networkConfig specifies VPC configuration for Private Service Connect.
	// Required for VPC configuration in Private Service Connect deployments.
	// +required
	NetworkConfig GCPNetworkConfig `json:"networkConfig"`

	// endpointAccess controls API endpoint accessibility for the HostedControlPlane on GCP.
	// Allowed values: "Private", "PublicAndPrivate". Defaults to "Private".
	// +kubebuilder:validation:Enum=PublicAndPrivate;Private
	// +kubebuilder:default=Private
	// +optional
	EndpointAccess GCPEndpointAccessType `json:"endpointAccess,omitempty"`

	// createDnsZones enables automatic DNS zone creation in the customer project.
	// When true, the control-plane-operator creates Cloud DNS zones for cluster ingress
	// and internal communication (hypershift.local). Defaults to false.
	// +kubebuilder:default=false
	// +optional
	CreateDNSZones bool `json:"createDnsZones,omitempty"`
}
