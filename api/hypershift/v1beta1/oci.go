package v1beta1

// OCIResourceReference represents a reference to an OCI resource by OCID.
// OCIDs (Oracle Cloud IDs) are unique identifiers for OCI resources.
// See https://docs.oracle.com/en-us/iaas/Content/General/Concepts/identifiers.htm for OCID format.
type OCIResourceReference struct {
	// ocid is the Oracle Cloud Identifier (OCID) for the resource.
	// OCIDs follow the format: ocid1.<resource_type>.<realm>.[region][.future_use].<unique_id>
	// The region segment may be empty for global resources (e.g., ocid1.tenancy.oc1..<unique_id>)
	// The unique_id segment is an opaque string assigned by OCI.
	// See https://docs.oracle.com/en-us/iaas/Content/General/Concepts/identifiers.htm for format details and examples.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	// +kubebuilder:validation:Pattern=`^ocid1\.[a-z0-9]+\.oc[0-9]+\.[a-z0-9-]*(?:\.[a-z0-9-]+)?\.[a-z0-9-]+$`
	OCID string `json:"ocid"`
}

// OCIVCNReference represents a reference to an OCI Virtual Cloud Network by OCID.
// VCNs are regional resources.
type OCIVCNReference struct {
	// ocid is the Oracle Cloud Identifier (OCID) for the VCN.
	// VCNs are regional resources and require a non-empty region segment.
	// Format: ocid1.vcn.<realm>.<region>[.future_use].<unique_id>
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	// +kubebuilder:validation:Pattern=`^ocid1\.vcn\.oc[0-9]+\.[a-z0-9-]+(?:\.[a-z0-9-]+)?\.[a-z0-9-]+$`
	OCID string `json:"ocid"`
}

// OCISubnetReference represents a reference to an OCI Subnet by OCID.
// Subnets are regional resources.
type OCISubnetReference struct {
	// ocid is the Oracle Cloud Identifier (OCID) for the subnet.
	// Subnets are regional resources and require a non-empty region segment.
	// Format: ocid1.subnet.<realm>.<region>[.future_use].<unique_id>
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	// +kubebuilder:validation:Pattern=`^ocid1\.subnet\.oc[0-9]+\.[a-z0-9-]+(?:\.[a-z0-9-]+)?\.[a-z0-9-]+$`
	OCID string `json:"ocid"`
}

// OCIEndpointAccessType defines the endpoint access type for OCI clusters.
// Determines how the hosted control plane API server is accessible.
type OCIEndpointAccessType string

const (
	// OCIEndpointAccessPublicAndPrivate endpoint access allows public API server access and
	// private node communication with the control plane within the VCN.
	OCIEndpointAccessPublicAndPrivate OCIEndpointAccessType = "PublicAndPrivate"

	// OCIEndpointAccessPrivate endpoint access allows only private API server access.
	// The control plane API is exposed via a private Network Load Balancer with a private IP.
	// Node communication with the control plane uses private connectivity within the VCN.
	OCIEndpointAccessPrivate OCIEndpointAccessType = "Private"
)

// OCINetworkConfig specifies VCN configuration for OCI clusters and private connectivity.
type OCINetworkConfig struct {
	// vcn is the Virtual Cloud Network (VCN) for the cluster.
	// +required
	VCN OCIVCNReference `json:"vcn"`

	// nlbSubnet is the subnet in the management VCN where the Network Load Balancer is deployed.
	// This subnet hosts the private NLB that fronts the hosted control plane API.
	// Required for private connectivity between customer VCNs (via Local Peering Gateway) and the hosted control plane.
	// +optional
	NLBSubnet *OCISubnetReference `json:"nlbSubnet,omitempty"`
}

// OCIPlatformSpec specifies configuration for clusters running on Oracle Cloud Infrastructure.
// +kubebuilder:validation:XValidation:rule="self.endpointAccess == 'Private' || self.endpointAccess == 'PublicAndPrivate' ? has(self.networkConfig.nlbSubnet) : true",message="nlbSubnet is required when endpointAccess is Private or PublicAndPrivate"
// +kubebuilder:validation:XValidation:rule="self.tenancy.split('.')[2] == self.compartment.split('.')[2]",message="tenancy and compartment must be in the same OCI realm"
// +kubebuilder:validation:XValidation:rule="self.networkConfig.vcn.ocid.split('.')[2] == self.tenancy.split('.')[2]",message="VCN OCID must be in the same OCI realm as tenancy"
// +kubebuilder:validation:XValidation:rule="!has(self.networkConfig.nlbSubnet) || self.networkConfig.nlbSubnet.ocid.split('.')[2] == self.tenancy.split('.')[2]",message="NLB subnet OCID must be in the same OCI realm as tenancy"
// +kubebuilder:validation:XValidation:rule="self.networkConfig.vcn.ocid.split('.')[3] == self.region",message="VCN OCID region must match spec.platform.oci.region"
// +kubebuilder:validation:XValidation:rule="!has(self.networkConfig.nlbSubnet) || self.networkConfig.nlbSubnet.ocid.split('.')[3] == self.region",message="NLB subnet OCID region must match spec.platform.oci.region"
type OCIPlatformSpec struct {
	// tenancy is the OCI tenancy OCID.
	// A tenancy is the root compartment that contains all cloud resources.
	// The OCID must be in the format: ocid1.tenancy.<realm>..<unique_id>
	// where <realm> identifies the OCI realm for your tenancy (e.g., oc1, oc2, oc3).
	// See https://docs.oracle.com/en-us/iaas/Content/General/Concepts/regions.htm for current realms.
	//
	// +required
	// +immutable
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	// +kubebuilder:validation:Pattern=`^ocid1\.tenancy\.oc[0-9]+\.\.[a-z0-9]+$`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Tenancy is immutable"
	Tenancy string `json:"tenancy"`

	// compartment is the OCI compartment OCID where cluster resources will be created.
	// A compartment is a logical container for organizing and isolating cloud resources.
	// The OCID must be in the format: ocid1.compartment.<realm>..<unique_id>
	// where <realm> identifies the OCI realm for your tenancy (e.g., oc1, oc2, oc3).
	// See https://docs.oracle.com/en-us/iaas/Content/General/Concepts/regions.htm for current realms.
	//
	// +required
	// +immutable
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	// +kubebuilder:validation:Pattern=`^ocid1\.compartment\.oc[0-9]+\.\.[a-z0-9]+$`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Compartment is immutable"
	Compartment string `json:"compartment"`

	// region is the OCI region in which the cluster resides.
	// A valid region must satisfy the following rules:
	//   format: Must be in the form `<location>-<city>-<number>` or `<location>-<realm>-<city>-<number>` for gov regions
	//   characters: Only lowercase letters (`a-z`), digits (`0-9`), and hyphens (`-`)
	//   valid examples: "us-ashburn-1", "uk-london-1", "ap-tokyo-1", "us-gov-ashburn-1", "uk-gov-london-1"
	// For a full list of valid regions, see: https://docs.oracle.com/en-us/iaas/Content/General/Concepts/regions.htm.
	//
	// +required
	// +immutable
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z]+(?:-[a-z]+){1,2}-[0-9]+$`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Region is immutable"
	Region string `json:"region"`

	// networkConfig specifies VCN configuration for the cluster.
	// Required for VCN configuration and private connectivity to the hosted control plane.
	// +required
	NetworkConfig OCINetworkConfig `json:"networkConfig"`

	// endpointAccess controls API endpoint accessibility for the HostedControlPlane on OCI.
	// Allowed values: "Private", "PublicAndPrivate". Defaults to "Private".
	// +kubebuilder:validation:Enum=PublicAndPrivate;Private
	// +kubebuilder:default=Private
	// +optional
	EndpointAccess OCIEndpointAccessType `json:"endpointAccess,omitempty"`
}
