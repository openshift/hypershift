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
	// - Contain only lowercase letters, digits, underscores, or hyphens
	// - End with a lowercase letter or digit (not a hyphen or underscore)
	// - Be 1-63 characters long
	// GCP reserves the 'goog' prefix for system labels.
	// See https://cloud.google.com/compute/docs/labeling-resources for Compute Engine label requirements.
	//
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z]([_a-z0-9-]{0,61}[a-z0-9])?$`
	// +kubebuilder:validation:XValidation:rule="!self.startsWith('goog')",message="Label keys starting with the reserved 'goog' prefix are not allowed"
	Key string `json:"key"`

	// value is the value part of the label. A label value can have a maximum of 63 characters.
	// Empty values are allowed by GCP. If non-empty, it must start with a lowercase letter,
	// contain only lowercase letters, digits, underscores, or hyphens, and end with a lowercase letter or digit.
	// See https://cloud.google.com/compute/docs/labeling-resources for Compute Engine label requirements.
	//
	// +optional
	// +kubebuilder:validation:MinLength=0
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^$|^[a-z]([_a-z0-9-]{0,61}[a-z0-9])?$`
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

// GCPProvisioningModel defines the provisioning model for GCP node instances.
// Follows GCP's provisioning model terminology for compute instances.
type GCPProvisioningModel string

const (
	// GCPProvisioningModelStandard specifies standard (non-preemptible) instances.
	// Standard instances run until explicitly stopped and are not subject to automatic termination.
	GCPProvisioningModelStandard GCPProvisioningModel = "Standard"

	// GCPProvisioningModelSpot specifies Spot instances.
	// Spot instances are lower-cost instances that can be terminated by GCP
	// with 30 seconds notice when capacity is needed elsewhere.
	// Unlike preemptible instances, Spot instances have no maximum runtime limit.
	// This is the recommended option for cost-effective, interruptible workloads.
	GCPProvisioningModelSpot GCPProvisioningModel = "Spot"

	// GCPProvisioningModelPreemptible specifies preemptible instances (legacy).
	// Preemptible instances are lower-cost instances that can be terminated by GCP
	// with 30 seconds notice when capacity is needed elsewhere.
	// Note: Preemptible instances have a maximum runtime of 24 hours.
	// Consider using Spot instances instead, which have no maximum runtime limit.
	GCPProvisioningModelPreemptible GCPProvisioningModel = "Preemptible"
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
// +kubebuilder:validation:XValidation:rule="self.workloadIdentity.serviceAccountsEmails.storage.contains('@') && self.workloadIdentity.serviceAccountsEmails.storage.endsWith('@' + self.project + '.iam.gserviceaccount.com')",message="storage service account must belong to the same project"
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
	// - roles/iam.serviceAccountUser (Service Account User - to attach service accounts to VMs)
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
	// - roles/dns.admin (DNS Admin - for managing DNS records)
	// - roles/compute.networkAdmin (Compute Network Admin - for network management)
	// - roles/compute.viewer (Compute Viewer - for CCM to read instance metadata)
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

	// storage is the Google Service Account email for the GCP PD CSI Driver
	// that manages Persistent Disk storage operations (create, attach, delete volumes).
	// This GSA requires the following IAM roles:
	// - roles/compute.storageAdmin (Compute Storage Admin - for managing persistent disks)
	// - roles/compute.instanceAdmin.v1 (Compute Instance Admin - for attaching disks to VMs)
	// - roles/iam.serviceAccountUser (Service Account User - for impersonation)
	// - roles/resourcemanager.tagUser (Tag User - for applying resource tags to disks)
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
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Storage is immutable"
	Storage string `json:"storage,omitempty"`
}

// GCPOnHostMaintenance defines the behavior when a host maintenance event occurs.
type GCPOnHostMaintenance string

const (
	// GCPOnHostMaintenanceMigrate causes Compute Engine to live migrate an instance during host maintenance.
	GCPOnHostMaintenanceMigrate GCPOnHostMaintenance = "MIGRATE"

	// GCPOnHostMaintenanceTerminate causes Compute Engine to stop an instance during host maintenance.
	GCPOnHostMaintenanceTerminate GCPOnHostMaintenance = "TERMINATE"
)

// GCPNodePoolPlatform specifies the configuration of a NodePool when operating on GCP.
// This follows the AWS and Azure patterns for platform-specific NodePool configuration.
// +kubebuilder:validation:XValidation:rule="!has(self.onHostMaintenance) || !has(self.provisioningModel) || self.provisioningModel == 'Standard' || self.onHostMaintenance == 'TERMINATE'",message="onHostMaintenance must be TERMINATE when provisioningModel is Spot or Preemptible"
type GCPNodePoolPlatform struct {
	// machineType is the GCP machine type for node instances (e.g. n2-standard-4).
	// Must follow GCP machine type naming conventions as documented at:
	// https://cloud.google.com/compute/docs/machine-resource#machine_type_comparison
	//
	// Valid machine type formats:
	//   - predefined: n1-standard-1, n2-highmem-4, c2-standard-8, etc.
	//   - custom: custom-{cpus}-{memory} (e.g. custom-4-8192)
	//   - custom with extended memory: custom-{cpus}-{memory}-ext (e.g. custom-2-13312-ext)
	//
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	// +kubebuilder:validation:Pattern=`^[a-z0-9]+(-[a-z0-9]+)*$`
	MachineType string `json:"machineType"`

	// zone is the GCP zone where node instances will be created.
	// Must be a valid zone within the cluster's region.
	// Format: {region}-{zone} (e.g. us-central1-a, europe-west2-b)
	// See https://cloud.google.com/compute/docs/regions-zones for available zones.
	//
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z]+(?:-[a-z0-9]+)*-[a-z]$`
	Zone string `json:"zone"`

	// subnet is the name of the subnet where node instances will be created.
	// Must be a subnet within the VPC network specified in the HostedCluster's
	// networkConfig and located in the same region as the zone.
	// The subnet must have enough IP addresses available for the expected number of nodes.
	//
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z]([-a-z0-9]*[a-z0-9])?$`
	Subnet string `json:"subnet"`

	// image specifies the boot image for node instances.
	// If unspecified, the default RHCOS image will be used based on the NodePool release payload.
	// Can be:
	//   - A family name: projects/rhel-cloud/global/images/family/rhel-8
	//   - A specific image: projects/rhel-cloud/global/images/rhel-8-v20231010
	//   - A full resource URL: https://www.googleapis.com/compute/v1/projects/rhel-cloud/global/images/rhel-8-v20231010
	//
	// +optional
	// +kubebuilder:validation:MaxLength=2048
	Image *string `json:"image,omitempty"`

	// bootDisk specifies the configuration for the boot disk of node instances.
	//
	// +optional
	BootDisk *GCPBootDisk `json:"bootDisk,omitempty"`

	// serviceAccount configures the Google Service Account attached to node instances.
	// If not specified, uses the default compute service account for the project.
	//
	// +optional
	ServiceAccount *GCPNodeServiceAccount `json:"serviceAccount,omitempty"`

	// resourceLabels is an optional list of additional labels to apply to GCP node
	// instances and their associated resources (disks, etc.).
	// Labels will be merged with cluster-level resource labels, with NodePool labels
	// taking precedence in case of conflicts.
	//
	// Keys and values must conform to GCP labeling requirements:
	//   - Keys: 1–63 chars, must start with a lowercase letter; allowed [a-z0-9_-]
	//   - Values: empty or 1–63 chars; allowed [a-z0-9_-]
	//   - Maximum 60 user labels per resource (GCP limit is 64 total, with ~4 reserved)
	//
	// +optional
	// +listType=map
	// +listMapKey=key
	// +kubebuilder:validation:MaxItems=60
	ResourceLabels []GCPResourceLabel `json:"resourceLabels,omitempty"`

	// networkTags is an optional list of network tags to apply to node instances.
	// These tags are used by GCP firewall rules to control network access.
	// Tags must conform to GCP naming conventions:
	//   - 1-63 characters
	//   - Lowercase letters, numbers, and hyphens only
	//   - Must start with lowercase letter
	//   - Cannot end with hyphen
	//
	// +optional
	// +listType=set
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=63
	// +kubebuilder:validation:items:Pattern=`^[a-z][a-z0-9-]*[a-z0-9]$|^[a-z]$`
	NetworkTags []string `json:"networkTags,omitempty"`

	// provisioningModel specifies the provisioning model for node instances.
	// Spot and Preemptible instances cost less but can be terminated by GCP with 30 seconds notice.
	// Spot instances are recommended over Preemptible as they have no maximum runtime limit.
	// Standard instances are regular VMs that run until explicitly stopped.
	// If not specified, defaults to "Standard".
	//
	// +optional
	// +kubebuilder:default=Standard
	// +kubebuilder:validation:Enum=Standard;Spot;Preemptible
	ProvisioningModel *GCPProvisioningModel `json:"provisioningModel,omitempty"`

	// onHostMaintenance specifies the behavior when host maintenance occurs.
	// For Spot and Preemptible instances, this must be "TERMINATE".
	// For Standard instances, can be "MIGRATE" (live migrate) or "TERMINATE".
	// If not specified, defaults to "MIGRATE" for Standard instances and "TERMINATE" for Spot/Preemptible.
	//
	// +optional
	// +kubebuilder:validation:Enum=MIGRATE;TERMINATE
	OnHostMaintenance *string `json:"onHostMaintenance,omitempty"`
}

// GCPBootDisk specifies configuration for the boot disk of GCP node instances.
type GCPBootDisk struct {
	// diskSizeGB specifies the size of the boot disk in gigabytes.
	// Must be at least 20 GB for RHCOS images.
	//
	// +optional
	// +kubebuilder:default=64
	// +kubebuilder:validation:Minimum=20
	// +kubebuilder:validation:Maximum=65536
	DiskSizeGB *int64 `json:"diskSizeGB,omitempty"`

	// diskType specifies the disk type for the boot disk.
	// Valid values include:
	//   - "pd-standard" - Standard persistent disk (magnetic)
	//   - "pd-ssd" - SSD persistent disk
	//   - "pd-balanced" - Balanced persistent disk (recommended)
	// If not specified, defaults to "pd-balanced".
	//
	// +optional
	// +kubebuilder:default="pd-balanced"
	// +kubebuilder:validation:Enum=pd-standard;pd-ssd;pd-balanced
	DiskType *string `json:"diskType,omitempty"`

	// encryptionKey specifies customer-managed encryption key (CMEK) configuration.
	// If not specified, Google-managed encryption keys are used.
	//
	// +optional
	EncryptionKey *GCPDiskEncryptionKey `json:"encryptionKey,omitempty"`
}

// GCPDiskEncryptionKey specifies configuration for customer-managed encryption keys.
type GCPDiskEncryptionKey struct {
	// kmsKeyName is the resource name of the Cloud KMS key used for disk encryption.
	// Format: projects/{project}/locations/{location}/keyRings/{keyRing}/cryptoKeys/{key}
	//
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:Pattern=`^projects\/[a-z][a-z0-9-]{4,28}[a-z0-9]\/locations\/[a-z0-9-]+\/keyRings\/[a-zA-Z0-9_-]+\/cryptoKeys\/[a-zA-Z0-9_-]+$`
	KMSKeyName string `json:"kmsKeyName"`
}

// GCPNodeServiceAccount specifies the Google Service Account configuration for node instances.
type GCPNodeServiceAccount struct {
	// email specifies the email address of the Google Service Account to use for node instances.
	// If not specified, uses the default compute service account for the project.
	// The service account must have the necessary permissions for the node to function:
	//   - Logging writer
	//   - Monitoring metric writer
	//   - Storage object viewer (for pulling container images)
	//
	// +optional
	// +kubebuilder:validation:MaxLength=254
	// +kubebuilder:validation:Pattern=`^[a-z][a-z0-9-]{4,28}[a-z0-9]@[a-z][a-z0-9-]{4,28}[a-z0-9]\.iam\.gserviceaccount\.com$`
	Email *string `json:"email,omitempty"`

	// scopes specifies the access scopes for the service account.
	// If not specified, defaults to standard compute scopes.
	// Common scopes include:
	//   - "https://www.googleapis.com/auth/devstorage.read_only" - Storage read-only
	//   - "https://www.googleapis.com/auth/logging.write" - Logging write
	//   - "https://www.googleapis.com/auth/monitoring.write" - Monitoring write
	//   - "https://www.googleapis.com/auth/cloud-platform" - Full access (use with caution)
	//
	// +optional
	// +listType=set
	// +kubebuilder:validation:MaxItems=50
	// +kubebuilder:validation:items:MaxLength=512
	Scopes []string `json:"scopes,omitempty"`
}
