package v1beta1

// GCPResourceReference represents a reference to a GCP resource by name.
// Follows GCP naming patterns (name-based APIs, not ID-based like AWS).
// See https://google.aip.dev/122 for GCP resource name standards.
type GCPResourceReference struct {
	// name is the name of the GCP resource.
	// Must conform to GCP resource naming standards: lowercase letters, numbers, and hyphens only.
	// Must start with a lowercase letter and end with a lowercase letter or number, max 63 characters.
	// Pattern: "^[a-z]([-a-z0-9]*[a-z0-9])?$" (max 63 chars), per GCP naming requirements.
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
	// For Compute Engine resources (VMs, disks, networks created by CAPG), keys must:
	// - Start with a lowercase letter
	// - Contain only lowercase letters, digits, or hyphens
	// - End with a lowercase letter or digit (not a hyphen)
	// - Be 1-63 characters long
	// GCP reserves the 'goog' prefix for system labels.
	// See https://cloud.google.com/compute/docs/labeling-resources for Compute Engine label requirements.
	//
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z]([-a-z0-9]{0,61}[a-z0-9])?$`
	// +kubebuilder:validation:XValidation:rule="!self.startsWith('goog')",message="Label keys starting with the reserved 'goog' prefix are not allowed"
	Key string `json:"key,omitempty"`

	// value is the value part of the label. A label value can have a maximum of 63 characters.
	// Empty values are allowed by GCP. If non-empty, it must start with a lowercase letter,
	// contain only lowercase letters, digits, or hyphens, and end with a lowercase letter or digit.
	// See https://cloud.google.com/compute/docs/labeling-resources for Compute Engine label requirements.
	//
	// +optional
	// +kubebuilder:validation:MinLength=0
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^$|^[a-z]([-a-z0-9]{0,61}[a-z0-9])?$`
	Value *string `json:"value,omitempty"`
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
// +kubebuilder:validation:XValidation:rule="self.workloadIdentity.serviceAccountsEmails.controlPlane.contains('@') && self.workloadIdentity.serviceAccountsEmails.controlPlane.endsWith('@' + self.project + '.iam.gserviceaccount.com')",message="controlPlane service account must belong to the same project"
// +kubebuilder:validation:XValidation:rule="self.workloadIdentity.serviceAccountsEmails.nodePool.contains('@') && self.workloadIdentity.serviceAccountsEmails.nodePool.endsWith('@' + self.project + '.iam.gserviceaccount.com')",message="nodePool service account must belong to the same project"
// +kubebuilder:validation:XValidation:rule="self.workloadIdentity.serviceAccountsEmails.cloudController.contains('@') && self.workloadIdentity.serviceAccountsEmails.cloudController.endsWith('@' + self.project + '.iam.gserviceaccount.com')",message="cloudController service account must belong to the same project"
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
	// Must be in the form of <geographic-area>-<location><number> (e.g., us-central1, europe-west12).
	// Must contain exactly one hyphen separating the geographic area from the location.
	// Must end with one or more digits.
	// Valid examples: "us-central1", "europe-west2", "europe-west12", "northamerica-northeast1"
	// Invalid examples: "us1" (no hyphen), "us-central" (no trailing digits), "us-central1-a" (zone suffix)
	// For a full list of valid regions, see: https://cloud.google.com/compute/docs/regions-zones.
	//
	// +required
	// +immutable
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z]+-[a-z]+[0-9]+$`
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
	// +listType=map
	// +listMapKey=key
	// +kubebuilder:validation:MaxItems=60
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
	WorkloadIdentity GCPWorkloadIdentityConfig `json:"workloadIdentity,omitzero"`
}

// GCPWorkloadIdentityConfig configures Workload Identity Federation for GCP clusters.
// This enables secure, short-lived token-based authentication without storing
// long-term service account keys.
type GCPWorkloadIdentityConfig struct {
	// projectNumber is the numeric GCP project identifier for WIF configuration.
	// This differs from the project ID and is required for workload identity pools.
	// Must be a numeric string representing the GCP project number.
	//
	// This is a user-provided value obtained from GCP (found in GCP Console or via `gcloud projects describe PROJECT_ID`).
	// Also available in the output of `hypershift infra create gcp`.
	//
	// +required
	// +immutable
	// +kubebuilder:validation:Pattern=`^[0-9]+$`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=25
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Project number is immutable"
	ProjectNumber string `json:"projectNumber,omitempty"`

	// poolID is the workload identity pool identifier within the project.
	// This pool is used to manage external identity mappings.
	// Must be 4-32 characters and start with a lowercase letter.
	// Allowed characters: lowercase letters (a-z), digits (0-9), hyphens (-).
	// Cannot start or end with a hyphen.
	// The prefix "gcp-" is reserved by Google and cannot be used.
	//
	// This is a user-provided value referencing a pre-created Workload Identity Pool.
	// Typically obtained from the output of `hypershift infra create gcp` which creates
	// the WIF infrastructure and generates appropriate pool IDs.
	//
	// +required
	// +immutable
	// +kubebuilder:validation:MinLength=4
	// +kubebuilder:validation:MaxLength=32
	// +kubebuilder:validation:Pattern=`^[a-z]([a-z0-9-]{2,30}[a-z0-9])$`
	// +kubebuilder:validation:XValidation:rule="!self.startsWith('gcp-')", message="Pool ID cannot start with reserved prefix 'gcp-'"
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Pool ID is immutable"
	PoolID string `json:"poolID,omitempty"`

	// providerID is the workload identity provider identifier within the pool.
	// This provider handles the token exchange between external and GCP identities.
	// Must be 4-32 characters and start with a lowercase letter.
	// Allowed characters: lowercase letters (a-z), digits (0-9), hyphens (-).
	// Cannot start or end with a hyphen.
	// The prefix "gcp-" is reserved by Google and cannot be used.
	//
	// This is a user-provided value referencing a pre-created OIDC Provider within the WIF Pool.
	// Typically obtained from the output of `hypershift infra create gcp`.
	//
	// +required
	// +immutable
	// +kubebuilder:validation:MinLength=4
	// +kubebuilder:validation:MaxLength=32
	// +kubebuilder:validation:Pattern=`^[a-z]([a-z0-9-]{2,30}[a-z0-9])$`
	// +kubebuilder:validation:XValidation:rule="!self.startsWith('gcp-')", message="Provider ID cannot start with reserved prefix 'gcp-'"
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Provider ID is immutable"
	ProviderID string `json:"providerID,omitempty"`

	// serviceAccountsEmails contains email addresses of various Google Service Accounts
	// required to enable integrations for different controllers and operators.
	// This follows the AWS pattern of having different roles for different purposes.
	//
	// +required
	// +immutable
	ServiceAccountsEmails GCPServiceAccountsEmails `json:"serviceAccountsEmails,omitzero"`
}

// GCPServiceAccountsEmails contains email addresses of Google Service Accounts for different controllers.
// Each service account should have the appropriate IAM permissions for its specific role.
type GCPServiceAccountsEmails struct {
	// nodePool is the Google Service Account email for CAPG controllers
	// that manage NodePool infrastructure (VMs, networks, disks, etc.).
	// This GSA requires the following IAM roles:
	// - roles/compute.instanceAdmin.v1 (Compute Instance Admin v1)
	// - roles/compute.networkAdmin (Compute Network Admin)
	// See cmd/infra/gcp/iam-bindings.json for the authoritative role definitions.
	// Format: service-account-name@project-id.iam.gserviceaccount.com
	//
	// This is a user-provided value referencing a pre-created Google Service Account.
	// Typically obtained from the output of `hypershift infra create gcp` which creates
	// the required service accounts with appropriate IAM roles and WIF bindings.
	//
	// +required
	// +immutable
	// +kubebuilder:validation:Pattern=`^[a-z][a-z0-9-]{4,28}[a-z0-9]@[a-z][a-z0-9-]{4,28}[a-z0-9]\.iam\.gserviceaccount\.com$`
	// +kubebuilder:validation:MinLength=38
	// +kubebuilder:validation:MaxLength=100
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="NodePool is immutable"
	NodePool string `json:"nodePool,omitempty"`

	// controlPlane is the Google Service Account email for the Control Plane Operator
	// that manages control plane infrastructure and resources.
	// This GSA requires the following IAM roles:
	// - roles/storage.admin (Storage Admin)
	// - roles/iam.serviceAccountUser (Service Account User)
	// See cmd/infra/gcp/iam-bindings.json for the authoritative role definitions.
	// Format: service-account-name@project-id.iam.gserviceaccount.com
	//
	// This is a user-provided value referencing a pre-created Google Service Account.
	// Typically obtained from the output of `hypershift infra create gcp` which creates
	// the required service accounts with appropriate IAM roles and WIF bindings.
	//
	// +required
	// +immutable
	// +kubebuilder:validation:Pattern=`^[a-z][a-z0-9-]{4,28}[a-z0-9]@[a-z][a-z0-9-]{4,28}[a-z0-9]\.iam\.gserviceaccount\.com$`
	// +kubebuilder:validation:MinLength=38
	// +kubebuilder:validation:MaxLength=100
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="ControlPlane is immutable"
	ControlPlane string `json:"controlPlane,omitempty"`

	// cloudController is the Google Service Account email for the Cloud Controller Manager
	// that manages LoadBalancer services and node lifecycle in the hosted cluster.
	// This GSA requires the following IAM roles:
	// - roles/compute.loadBalancerAdmin (Load Balancer Admin - for provisioning GCP load balancers)
	// - roles/compute.securityAdmin (Security Admin - for managing firewall rules)
	// - roles/compute.viewer (Compute Viewer - for reading instance metadata for node management)
	// See cmd/infra/gcp/iam-bindings.json for the authoritative role definitions.
	// Format: service-account-name@project-id.iam.gserviceaccount.com
	//
	// This is a user-provided value referencing a pre-created Google Service Account.
	// Typically obtained from the output of `hypershift infra create gcp` which creates
	// the required service accounts with appropriate IAM roles and WIF bindings.
	//
	// +required
	// +immutable
	// +kubebuilder:validation:Pattern=`^[a-z][a-z0-9-]{4,28}[a-z0-9]@[a-z][a-z0-9-]{4,28}[a-z0-9]\.iam\.gserviceaccount\.com$`
	// +kubebuilder:validation:MinLength=38
	// +kubebuilder:validation:MaxLength=100
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="CloudController is immutable"
	CloudController string `json:"cloudController,omitempty"`
}
