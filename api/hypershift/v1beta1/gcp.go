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

// GCPResourceLabel is a label to apply to GCP resources created for the cluster.
// Labels are key-value pairs used for organizing and managing GCP resources.
// See https://cloud.google.com/compute/docs/labeling-resources for GCP labeling guidance.
type GCPResourceLabel struct {
	// key is the key part of the label. A label key can have a maximum of 63 characters and cannot be empty.
	// For Compute Engine resources (VMs, disks, networks created by CAPG), keys must follow RFC1035-style rules:
	// - Start with a lowercase letter
	// - Contain only lowercase letters, digits, or hyphens
	// - Cannot end with a hyphen
	// GCP reserves the 'goog' prefix for system labels.
	// Note: Other GCP services may allow a broader character set (e.g., underscores); consult service-specific documentation.
	// See https://cloud.google.com/compute/docs/labeling-resources for Compute Engine label requirements.
	//
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z]([0-9a-z-]{0,61}[0-9a-z])?$`
	// +kubebuilder:validation:XValidation:rule="!self.startsWith('goog')",message="Label keys starting with the reserved 'goog' prefix are not allowed"
	Key string `json:"key"`

	// value is the value part of the label. A label value can have a maximum of 63 characters.
	// Empty values are allowed by GCP.
	// For Compute Engine resources (VMs, disks, networks created by CAPG), values must follow RFC1035-style rules:
	// - Contain only lowercase letters, digits, or hyphens
	// - If non-empty, must start with a lowercase letter or digit
	// - If non-empty, cannot end with a hyphen
	// Note: Other GCP services may allow a broader character set (e.g., underscores); consult service-specific documentation.
	// See https://cloud.google.com/compute/docs/labeling-resources for Compute Engine label requirements.
	//
	// +required
	// +kubebuilder:validation:MinLength=0
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^$|^[0-9a-z]([0-9a-z-]{0,61}[0-9a-z])?$`
	Value string `json:"value"`
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
	//   format: Must be in the form `<letters>-<segment>...` and must end with one or more digits
	//   characters: Only lowercase letters (`a-z`), digits (`0-9`), and hyphens (`-`) as separators
	//   requirements: Must contain at least one hyphen; must end with digits
	//   valid examples: "us-central1", "europe-west2", "europe-west12", "northamerica-northeast1"
	//   invalid examples: "us1" (no hyphen), "us-central" (no trailing digits), "us-central1-a" (zone suffix)
	// For a full list of valid regions, see: https://cloud.google.com/compute/docs/regions-zones.
	//
	// +required
	// +immutable
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z]+(-[a-z0-9]+)+[0-9]+$`
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
	// Labels are key-value pairs used for organizing and managing GCP resources.
	// Changes to this field will be propagated in-place to GCP resources where supported.
	// GCP supports a maximum of 64 labels per resource. HyperShift reserves approximately 4 labels for system use.
	// For GCP labeling guidance, see https://cloud.google.com/compute/docs/labeling-resources
	//
	// +optional
	// +kubebuilder:validation:MaxItems=64
	ResourceLabels []GCPResourceLabel `json:"resourceLabels,omitempty"`

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
	// Must be 4-32 characters; allowed: lowercase letters (a-z), digits (0-9), hyphens (-).
	// Cannot start or end with a hyphen.
	// The prefix "gcp-" is reserved by Google and cannot be used.
	//
	// Note: HyperShift generates this value. Validation is intentionally stricter
	// than Google's minimum (e.g., disallow leading/trailing hyphens) because we
	// fully control the ID format. If we later allow user-provided IDs, consider
	// relaxing to ^[a-z0-9-]{4,32}$ and keep the 'gcp-' reserved-prefix check.
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
	// Must be 4-32 characters; allowed: lowercase letters (a-z), digits (0-9), hyphens (-).
	// Cannot start or end with a hyphen.
	// The prefix "gcp-" is reserved by Google and cannot be used.
	//
	// Note: See poolID above regarding intentionally stricter validation for generated IDs.
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
