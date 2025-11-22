package v1beta1

// OCIResourceReference represents a reference to an OCI resource by OCID.
// OCIDs (Oracle Cloud IDs) are unique identifiers for OCI resources.
// See https://docs.oracle.com/en-us/iaas/Content/General/Concepts/identifiers.htm for OCID format.
type OCIResourceReference struct {
	// ocid is the Oracle Cloud Identifier (OCID) for the resource.
	// OCIDs follow the format: ocid1.<resource_type>.<realm>.[region][.future_use].<unique_id>
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	// +kubebuilder:validation:Pattern=`^ocid1\.[a-z0-9]+\.(oc[0-9]+|region)\.[a-z0-9.-]*\.[a-z0-9]+$`
	OCID string `json:"ocid"`
}

// OCIEndpointAccessType defines the endpoint access type for OCI clusters.
// Determines how the hosted control plane API server is accessible.
type OCIEndpointAccessType string

const (
	// OCIEndpointAccessPublicAndPrivate endpoint access allows public API server access and
	// private node communication with the control plane via Service Gateway.
	OCIEndpointAccessPublicAndPrivate OCIEndpointAccessType = "PublicAndPrivate"

	// OCIEndpointAccessPrivate endpoint access allows only private API server access and private
	// node communication with the control plane via Service Gateway and Network Load Balancer.
	OCIEndpointAccessPrivate OCIEndpointAccessType = "Private"
)

// OCINetworkConfig specifies VCN configuration for OCI clusters and Service Gateway connectivity.
type OCINetworkConfig struct {
	// vcn is the Virtual Cloud Network (VCN) for the cluster.
	// +required
	VCN OCIResourceReference `json:"vcn"`

	// serviceGatewaySubnet is the subnet in the management VCN for Service Gateway endpoints.
	// This subnet is used for private connectivity between customer VCNs and the hosted control plane.
	// +optional
	ServiceGatewaySubnet *OCIResourceReference `json:"serviceGatewaySubnet,omitempty"`
}

// OCIPlatformSpec specifies configuration for clusters running on Oracle Cloud Infrastructure.
type OCIPlatformSpec struct {
	// tenancy is the OCI tenancy OCID.
	// A tenancy is the root compartment that contains all cloud resources.
	// The OCID must be in the format: ocid1.tenancy.oc1..<unique_id>
	//
	// +required
	// +immutable
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	// +kubebuilder:validation:Pattern=`^ocid1\.tenancy\.oc1\.\.[a-z0-9]+$`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Tenancy is immutable"
	Tenancy string `json:"tenancy"`

	// compartment is the OCI compartment OCID where cluster resources will be created.
	// A compartment is a logical container for organizing and isolating cloud resources.
	// The OCID must be in the format: ocid1.compartment.oc1..<unique_id>
	//
	// +required
	// +immutable
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	// +kubebuilder:validation:Pattern=`^ocid1\.(tenancy|compartment)\.oc1\.\.[a-z0-9]+$`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Compartment is immutable"
	Compartment string `json:"compartment"`

	// region is the OCI region in which the cluster resides.
	// A valid region must satisfy the following rules:
	//   format: Must be in the form `<location>-<city>-<number>`
	//   characters: Only lowercase letters (`a-z`), digits (`0-9`), and hyphens (`-`)
	//   valid examples: "us-ashburn-1", "uk-london-1", "ap-tokyo-1"
	// For a full list of valid regions, see: https://docs.oracle.com/en-us/iaas/Content/General/Concepts/regions.htm.
	//
	// +required
	// +immutable
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z]+-[a-z]+-[0-9]+$`
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="Region is immutable"
	Region string `json:"region"`

	// networkConfig specifies VCN configuration for Service Gateway connectivity.
	// Required for VPC configuration and private connectivity to the hosted control plane.
	// +required
	NetworkConfig OCINetworkConfig `json:"networkConfig"`

	// endpointAccess controls API endpoint accessibility for the HostedControlPlane on OCI.
	// Allowed values: "Private", "PublicAndPrivate". Defaults to "Private".
	// +kubebuilder:validation:Enum=PublicAndPrivate;Private
	// +kubebuilder:default=Private
	// +optional
	EndpointAccess OCIEndpointAccessType `json:"endpointAccess,omitempty"`
}
