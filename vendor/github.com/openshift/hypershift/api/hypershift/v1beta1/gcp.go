package v1beta1

// GCPResourceReference represents a reference to a GCP resource by name.
// Follows GCP naming patterns (name-based APIs, not ID-based like AWS).
// See https://google.aip.dev/122 for GCP resource name standards.
type GCPResourceReference struct {
	// name is the name of the GCP resource.
	// Must conform to GCP resource naming standards: lowercase letters, numbers, and hyphens only.
	// Must start with lowercase letter and end with lowercase letter or number, max 63 characters.
	// Pattern: ^[a-z]([-a-z0-9]*[a-z0-9])?$ per GCP naming requirements.
	// See https://cloud.google.com/compute/docs/naming-resources for details.
	//
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z]([-a-z0-9]*[a-z0-9])?$`
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
	// +immutable
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Network is immutable"
	Network GCPResourceReference `json:"network"`

	// privateServiceConnectSubnet is the subnet for Private Service Connect endpoints
	// +required
	// +immutable
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Private Service Connect subnet is immutable"
	PrivateServiceConnectSubnet GCPResourceReference `json:"privateServiceConnectSubnet"`
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
	// +kubebuilder:validation:MinLength=6
	// +kubebuilder:validation:MaxLength=30
	// +kubebuilder:validation:Pattern=`^[a-z]([a-z0-9-]{4,28}[a-z0-9])$`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Project is immutable"
	Project string `json:"project"`

	// region is the GCP region in which the cluster resides.
	// A valid region must satisfy the following rules:
	//   format: Must be in the form `<letters>-<segment1>-<segment2>-...<digit>`
	//   characters: Only lowercase letters (`a-z`), digits (`0-9`), and hyphens (`-`) as separators
	//   valid examples: "us-central1", "europe-west2", "northamerica-northeast1"
	//   region must not include zone suffixes (e.g., "-a").
	// For a full list of valid regions, see: https://cloud.google.com/compute/docs/regions-zones.
	//
	// +required
	// +immutable
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z]+(?:-[a-z0-9]+)*[0-9]$`
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

	// resourceLabels are applied to all GCP resources created for the cluster.
	// These labels help with resource organization, cost tracking, and management.
	// Keys and values must conform to GCP label requirements:
	//   - Keys: 1-63 characters, must start with lowercase letter or international character
	//   - Values: 0-63 characters (may be empty)
	//   - Characters: lowercase letters, numeric characters, underscores, dashes, UTF-8 encoding
	//   - Maximum 64 labels per resource
	// See https://cloud.google.com/compute/docs/labeling-resources
	//
	// +optional
	// +kubebuilder:validation:MaxProperties=64
	// +kubebuilder:validation:XValidation:rule="keys(self).all(k, size(k) >= 1 && size(k) <= 63)", message="All label keys must be 1-63 characters"
	// +kubebuilder:validation:XValidation:rule="keys(self).all(k, size(self[k]) <= 63)", message="All label values must be at most 63 characters"
	// +kubebuilder:validation:XValidation:rule="keys(self).all(k, k.matches('^[a-z][a-z0-9_-]{0,62}$'))", message="Label keys must start with lowercase letter and contain only lowercase letters, numbers, underscores, and dashes"
	// +kubebuilder:validation:XValidation:rule="keys(self).all(k, self[k] == '' || self[k].matches('^[a-z][a-z0-9_-]{0,62}$'))", message="Label values must be empty or start with lowercase letter and contain only lowercase letters, numbers, underscores, and dashes"
	ResourceLabels map[string]string `json:"resourceLabels,omitempty"`

	// workloadIdentity configures Workload Identity Federation for the cluster.
	// This enables secure, short-lived token-based authentication without storing
	// long-term service account keys. These fields are immutable after cluster creation
	// to prevent breaking the authentication chain.
	//
	// Prerequisites for WIF setup:
	//   - Workload Identity Pool and Provider must exist in the GCP project
	//   - Provider must be configured with audience mapping for OpenShift SA tokens
	//   - Target Google Service Account must have roles/iam.workloadIdentityUser
	//     granted to the workload pool principal (e.g., principal://iam.googleapis.com/projects/PROJECT_NUMBER/locations/global/workloadIdentityPools/POOL_ID/subject/system:serviceaccount:kube-system:capi-gcp-controller-manager)
	//   - Attribute mappings on the provider should include google.subject for token subject verification
	//
	// +required
	// +immutable
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="WorkloadIdentity is immutable"
	WorkloadIdentity GCPWorkloadIdentityConfig `json:"workloadIdentity"`
}

// GCPWorkloadIdentityConfig configures Workload Identity Federation for GCP clusters.
// This enables secure, short-lived token-based authentication without storing
// long-term service account keys.
type GCPWorkloadIdentityConfig struct {
	// projectNumber is the numeric GCP project identifier for WIF configuration.
	// This differs from the project ID and is required for workload identity pools.
	// Must be a numeric string representing the GCP project number.
	//
	// +required
	// +immutable
	// +kubebuilder:validation:Pattern=`^[0-9]+$`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=25
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Project number is immutable"
	ProjectNumber string `json:"projectNumber"`

	// poolID is the workload identity pool identifier within the project.
	// This pool is used to manage external identity mappings.
	// Must be 4-32 characters, lowercase letters, numbers, and hyphens only.
	// Cannot start or end with a hyphen.
	//
	// +required
	// +immutable
	// +kubebuilder:validation:MinLength=4
	// +kubebuilder:validation:MaxLength=32
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([a-z0-9-]{2,30}[a-z0-9])$`
	// +kubebuilder:validation:XValidation:rule="!self.startsWith('gcp-')", message="Pool ID cannot start with reserved prefix 'gcp-'"
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Pool ID is immutable"
	PoolID string `json:"poolID"`

	// providerID is the workload identity provider identifier within the pool.
	// This provider handles the token exchange between external and GCP identities.
	// Must be 4-32 characters, lowercase letters, numbers, and hyphens only.
	// Cannot start or end with a hyphen.
	//
	// +required
	// +immutable
	// +kubebuilder:validation:MinLength=4
	// +kubebuilder:validation:MaxLength=32
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([a-z0-9-]{2,30}[a-z0-9])$`
	// +kubebuilder:validation:XValidation:rule="!self.startsWith('gcp-')", message="Provider ID cannot start with reserved prefix 'gcp-'"
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Provider ID is immutable"
	ProviderID string `json:"providerID"`

	// serviceAccountsRef contains references to various Google Service Accounts
	// required to enable integrations for different controllers and operators.
	// This follows the AWS pattern of having different roles for different purposes.
	//
	// +required
	// +immutable
	ServiceAccountsRef GCPServiceAccountsRef `json:"serviceAccountsRef"`
}

// GCPServiceAccountsRef contains references to Google Service Accounts for different controllers.
// Each service account should have the appropriate IAM permissions for its specific role.
type GCPServiceAccountsRef struct {
	// nodePoolEmail is the Google Service Account email for CAPG controllers
	// that manage NodePool infrastructure (VMs, networks, disks, etc.).
	// This GSA needs compute.*, network.*, and storage.* permissions.
	// Format: service-account-name@project-id.iam.gserviceaccount.com
	//
	// +required
	// +immutable
	// +kubebuilder:validation:Pattern=`^[a-z][a-z0-9-]{4,28}[a-z0-9]@[a-z][a-z0-9-]{4,28}[a-z0-9]\.iam\.gserviceaccount\.com$`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=100
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="NodePool email is immutable"
	NodePoolEmail string `json:"nodePoolEmail"`
}
