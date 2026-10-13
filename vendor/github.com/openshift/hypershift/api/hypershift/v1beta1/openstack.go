package v1beta1

// PortSecurityPolicy defines whether or not to enable port security on a port.
type PortSecurityPolicy string

const (
	// PortSecurityEnabled enables port security on a port.
	PortSecurityEnabled PortSecurityPolicy = "Enabled"

	// PortSecurityDisabled disables port security on a port.
	PortSecurityDisabled PortSecurityPolicy = "Disabled"

	// PortSecurityDefault uses the default port security policy.
	PortSecurityDefault PortSecurityPolicy = ""
)

type OpenStackNodePoolPlatform struct {
	// flavor is the OpenStack flavor to use for the node instances.
	//
	// +required
	// +kubebuilder:validation:MaxLength=255
	Flavor string `json:"flavor"`

	// imageName is the OpenStack Glance image name to use for node instances. If unspecified, the default
	// is chosen based on the NodePool release payload image.
	//
	// +optional
	// +kubebuilder:validation:MaxLength=255
	ImageName string `json:"imageName,omitempty"`

	// availabilityZone is the nova availability zone in which the provider will create the VM.
	// If not specified, the VM will be created in the default availability zone specified in the nova configuration.
	// Availability zone names must NOT contain : since it is used by admin users to specify hosts where instances
	// are launched in server creation. Also, it must not contain spaces otherwise it will lead to node that belongs
	// to this availability zone register failure, see kubernetes/cloud-provider-openstack#1379 for further information.
	// The maximum length of availability zone name is 63 as per labels limits.
	//
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^[^: ]*$`
	// +kubebuilder:validation:MaxLength=63
	// +optional
	AvailabilityZone string `json:"availabilityZone,omitempty"`

	// additionalPorts is a list of additional ports to create on the node instances.
	//
	// +optional
	// +kubebuilder:validation:MaxItems=10
	AdditionalPorts []PortSpec `json:"additionalPorts,omitempty"`
}

// OpenStackPlatformSpec specifies configuration for clusters running on OpenStack.
type OpenStackPlatformSpec struct {
	// identityRef is a reference to a secret holding OpenStack credentials
	// to be used when reconciling the hosted cluster.
	//
	// +required
	IdentityRef OpenStackIdentityReference `json:"identityRef"`

	// managedSubnets describe the OpenStack Subnet to be created. Cluster actuator will create a network,
	// and a subnet with the defined DNSNameservers, AllocationPools and the CIDR defined in the HostedCluster
	// MachineNetwork, and a router connected to the subnet. Currently only one IPv4
	// subnet is supported.
	//
	// +kubebuilder:validation:MaxItems=1
	// +listType=atomic
	// +optional
	ManagedSubnets []SubnetSpec `json:"managedSubnets,omitempty"`

	// router specifies an existing router to be used if ManagedSubnets are
	// specified. If specified, no new router will be created.
	//
	// +optional
	Router *RouterParam `json:"router,omitempty"`

	// network specifies an existing network to use if no ManagedSubnets
	// are specified.
	// +optional
	Network *NetworkParam `json:"network,omitempty"`

	// subnets specifies existing subnets to use if not ManagedSubnets are
	// specified. All subnets must be in the network specified by Network.
	// There can be zero, one, or two subnets. If no subnets are specified,
	// all subnets in Network will be used. If 2 subnets are specified, one
	// must be IPv4 and the other IPv6.
	//
	// +kubebuilder:validation:MaxItems=2
	// +listType=atomic
	// +optional
	Subnets []SubnetParam `json:"subnets,omitempty"`

	// networkMTU is the MTU for the network.
	// +kubebuilder:validation:Minimum=1500
	// +kubebuilder:validation:Maximum=9000
	// +optional
	NetworkMTU *int `json:"networkMTU,omitempty"`

	// externalNetwork is the OpenStack Network to be used to get public internet to the VMs.
	// This option is ignored if DisableExternalNetwork is set to true.
	//
	// If ExternalNetwork is defined it must refer to exactly one external network.
	//
	// If ExternalNetwork is not defined or is empty the controller will use any
	// existing external network as long as there is only one. It is an
	// error if ExternalNetwork is not defined and there are multiple
	// external networks unless DisableExternalNetwork is also set.
	//
	// If ExternalNetwork is not defined and there are no external networks
	// the controller will proceed as though DisableExternalNetwork was set.
	//
	// +optional
	ExternalNetwork *NetworkParam `json:"externalNetwork,omitempty"`

	// disableExternalNetwork specifies whether or not to attempt to connect the cluster
	// to an external network. This allows for the creation of clusters when connecting
	// to an external network is not possible or desirable, e.g. if using a provider network.
	//
	// +optional
	DisableExternalNetwork *bool `json:"disableExternalNetwork,omitempty"`

	// tags to set on all resources in cluster which support tags
	//
	// +listType=set
	// +optional
	// +kubebuilder:validation:MaxItems=50
	// +kubebuilder:validation:items:MaxLength=255
	Tags []string `json:"tags,omitempty"`

	// ingressFloatingIP is an available floating IP in your OpenStack cluster that will
	// be associated with the OpenShift ingress port.
	// When not specified, an IP address will be assigned randomly by the OpenStack cloud provider.
	// When specified, the floating IP has to be pre-created.  If the
	// specified value is not a floating IP or is already claimed, the
	// OpenStack cloud provider won't be able to provision the load
	// balancer.
	// This value must be a valid IPv4 or IPv6 address.
	//
	// +kubebuilder:validation:XValidation:rule="isIP(self)",message="floatingIP must be a valid IPv4 or IPv6 address"
	// +kubebuilder:validation:MaxLength=45
	// +optional
	IngressFloatingIP string `json:"ingressFloatingIP,omitempty"`
}

// OpenStackIdentityReference is a reference to an infrastructure
// provider identity to be used to provision cluster resources.
type OpenStackIdentityReference struct {
	// name is the name of a secret in the same namespace as the resource being provisioned.
	// The secret must contain a key named `clouds.yaml` which contains an OpenStack clouds.yaml file.
	// The secret may optionally contain a key named `cacert` containing a PEM-encoded CA certificate.
	//
	// +required
	// +kubebuilder:validation:MaxLength=255
	Name string `json:"name"`

	// cloudName specifies the name of the entry in the clouds.yaml file to use.
	//
	// +required
	// +kubebuilder:validation:MaxLength=255
	CloudName string `json:"cloudName"`
}

type SubnetSpec struct {
	// dnsNameservers holds a list of DNS server addresses that will be provided when creating
	// the subnet. These addresses need to have the same IP version as CIDR.
	//
	// +optional
	// +kubebuilder:validation:MaxItems=10
	// +kubebuilder:validation:items:MaxLength=45
	DNSNameservers []string `json:"dnsNameservers,omitempty"`

	// allocationPools is an array of AllocationPool objects that will be applied to OpenStack Subnet being created.
	// If set, OpenStack will only allocate these IPs for Machines. It will still be possible to create ports from
	// outside of these ranges manually.
	//
	// +optional
	// +kubebuilder:validation:MaxItems=10
	AllocationPools []AllocationPool `json:"allocationPools,omitempty"`
}

type AllocationPool struct {
	// start represents the start of the AllocationPool, that is the lowest IP of the pool.
	//
	// +required
	// +kubebuilder:validation:MaxLength=255
	Start string `json:"start"`

	// end represents the end of the AlloctionPool, that is the highest IP of the pool.
	//
	// +required
	// +kubebuilder:validation:MaxLength=255
	End string `json:"end"`
}

// RouterParam specifies an OpenStack router to use. It may be specified by either ID or filter, but not both.
// +kubebuilder:validation:MaxProperties:=1
// +kubebuilder:validation:MinProperties:=1
type RouterParam struct {
	// id is the ID of the router to use. If ID is provided, the other filters cannot be provided. Must be in UUID format.
	//
	// +kubebuilder:validation:Format:=uuid
	// +optional
	// +kubebuilder:validation:MaxLength=36
	ID *string `json:"id,omitempty"`

	// filter specifies a filter to select an OpenStack router. If provided, cannot be empty.
	//
	// +optional
	Filter *RouterFilter `json:"filter,omitempty"`
}

// RouterFilter specifies a query to select an OpenStack router. At least one property must be set.
// +kubebuilder:validation:MinProperties:=1
type RouterFilter struct {
	// name is the name of the router to filter by.
	//
	// +optional
	// +kubebuilder:validation:MaxLength=255
	Name string `json:"name,omitempty"`

	// description is the description of the router to filter by.
	//
	// +optional
	// +kubebuilder:validation:MaxLength=255
	Description string `json:"description,omitempty"`

	// projectID is the project ID of the router to filter by.
	//
	// +optional
	// +kubebuilder:validation:MaxLength=255
	ProjectID string `json:"projectID,omitempty"`

	// FilterByNeutronTags specifies tags to filter by.
	//
	// +optional
	FilterByNeutronTags `json:",inline"`
}

// NetworkParam specifies an OpenStack network. It may be specified by either ID or Filter, but not both.
// +kubebuilder:validation:MaxProperties:=1
// +kubebuilder:validation:MinProperties:=1
type NetworkParam struct {
	// id is the ID of the network to use. If ID is provided, the other filters cannot be provided. Must be in UUID format.
	//
	// +kubebuilder:validation:Format:=uuid
	// +optional
	// +kubebuilder:validation:MaxLength=36
	ID *string `json:"id,omitempty"`

	// filter specifies a filter to select an OpenStack network. If provided, cannot be empty.
	//
	// +optional
	Filter *NetworkFilter `json:"filter,omitempty"`
}

// NetworkFilter specifies a query to select an OpenStack network. At least one property must be set.
// +kubebuilder:validation:MinProperties:=1
type NetworkFilter struct {
	// name is the name of the network to filter by.
	//
	// +optional
	// +kubebuilder:validation:MaxLength=255
	Name string `json:"name,omitempty"`

	// description is the description of the network to filter by.
	//
	// +optional
	// +kubebuilder:validation:MaxLength=255
	Description string `json:"description,omitempty"`

	// projectID is the project ID of the network to filter by.
	//
	// +optional
	// +kubebuilder:validation:MaxLength=255
	ProjectID string `json:"projectID,omitempty"`

	// FilterByNeutronTags specifies tags to filter by.
	//
	// +optional
	FilterByNeutronTags `json:",inline"`
}

// NeutronTag represents a tag on a Neutron resource.
// It may not be empty and may not contain commas.
// +kubebuilder:validation:Pattern:="^[^,]+$"
// +kubebuilder:validation:MinLength:=1
// +kubebuilder:validation:MaxLength=255
type NeutronTag string

type FilterByNeutronTags struct {
	// tags is a list of tags to filter by. If specified, the resource must
	// have all of the tags specified to be included in the result.
	//
	// +listType=set
	// +optional
	// +kubebuilder:validation:MaxItems=50
	Tags []NeutronTag `json:"tags,omitempty"`

	// tagsAny is a list of tags to filter by. If specified, the resource
	// must have at least one of the tags specified to be included in the
	// result.
	//
	// +listType=set
	// +optional
	// +kubebuilder:validation:MaxItems=50
	TagsAny []NeutronTag `json:"tagsAny,omitempty"`

	// notTags is a list of tags to filter by. If specified, resources which
	// contain all of the given tags will be excluded from the result.
	//
	// +listType=set
	// +optional
	// +kubebuilder:validation:MaxItems=50
	NotTags []NeutronTag `json:"notTags,omitempty"`

	// notTagsAny is a list of tags to filter by. If specified, resources
	// which contain any of the given tags will be excluded from the result.
	//
	// +listType=set
	// +optional
	// +kubebuilder:validation:MaxItems=50
	NotTagsAny []NeutronTag `json:"notTagsAny,omitempty"`
}

// SubnetParam specifies an OpenStack subnet to use. It may be specified by either ID or filter, but not both.
// +kubebuilder:validation:MaxProperties:=1
// +kubebuilder:validation:MinProperties:=1
type SubnetParam struct {
	// id is the uuid of the subnet. It will not be validated.
	//
	// +kubebuilder:validation:Format:=uuid
	// +optional
	// +kubebuilder:validation:MaxLength=36
	ID *string `json:"id,omitempty"`

	// filter specifies a filter to select the subnet. It must match exactly one subnet.
	//
	// +optional
	Filter *SubnetFilter `json:"filter,omitempty"`
}

// SubnetFilter specifies a filter to select a subnet. At least one parameter must be specified.
// +kubebuilder:validation:MinProperties:=1
type SubnetFilter struct {
	// name is the name of the subnet to filter by.
	//
	// +optional
	// +kubebuilder:validation:MaxLength=255
	Name string `json:"name,omitempty"`
	// description is the description of the subnet to filter by.
	//
	// +optional
	// +kubebuilder:validation:MaxLength=255
	Description string `json:"description,omitempty"`

	// projectID is the project ID of the subnet to filter by.
	//
	// +optional
	// +kubebuilder:validation:MaxLength=255
	ProjectID string `json:"projectID,omitempty"`

	// ipVersion is the IP version of the subnet to filter by.
	//
	// +optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=2147483647
	IPVersion int `json:"ipVersion,omitempty"`

	// gatewayIP is the gateway IP of the subnet to filter by.
	//
	// +optional
	// +kubebuilder:validation:MaxLength=45
	GatewayIP string `json:"gatewayIP,omitempty"`

	// cidr is the CIDR of the subnet to filter by.
	//
	// +optional
	// +kubebuilder:validation:MaxLength=45
	CIDR string `json:"cidr,omitempty"`

	// ipv6AddressMode is the IPv6 address mode of the subnet to filter by.
	//
	// +optional
	// +kubebuilder:validation:MaxLength=255
	IPv6AddressMode string `json:"ipv6AddressMode,omitempty"`

	// ipv6RAMode is the IPv6 RA mode of the subnet to filter by.
	//
	// +optional
	// +kubebuilder:validation:MaxLength=255
	IPv6RAMode string `json:"ipv6RAMode,omitempty"`

	// FilterByNeutronTags specifies tags to filter by.
	//
	// +optional
	FilterByNeutronTags `json:",inline"`
}

// PortSpec specifies the options for creating a port.
type PortSpec struct {
	// network is a query for an openstack network that the port will be created or discovered on.
	// This will fail if the query returns more than one network.
	//
	// +optional
	Network *NetworkParam `json:"network,omitempty"`

	// description is a human-readable description for the port.
	//
	// +optional
	// +kubebuilder:validation:MaxLength=255
	Description string `json:"description,omitempty"`

	// allowedAddressPairs is a list of address pairs which Neutron will
	// allow the port to send traffic from in addition to the port's
	// addresses. If not specified, the MAC Address will be the MAC Address
	// of the port. Depending on the configuration of Neutron, it may be
	// supported to specify a CIDR instead of a specific IP address.
	//
	// +optional
	// +kubebuilder:validation:MaxItems=10
	AllowedAddressPairs []AddressPair `json:"allowedAddressPairs,omitempty"`

	// vnicType specifies the type of vNIC which this port should be
	// attached to. This is used to determine which mechanism driver(s) to
	// be used to bind the port. The valid values are normal, macvtap,
	// direct, baremetal, direct-physical, virtio-forwarder, smart-nic and
	// remote-managed, although these values will not be validated in this
	// API to ensure compatibility with future neutron changes or custom
	// implementations. What type of vNIC is actually available depends on
	// deployments. If not specified, the Neutron default value is used.
	//
	// +optional
	// +kubebuilder:validation:MaxLength=255
	VNICType string `json:"vnicType,omitempty"`

	// portSecurityPolicy specifies whether or not to enable port security on the port.
	// Allowed values are "Enabled", "Disabled" and omitted.
	// When not set, it takes the value of the corresponding field at the network level.
	//
	// +kubebuilder:validation:Enum:=Enabled;Disabled;""
	// +optional
	PortSecurityPolicy PortSecurityPolicy `json:"portSecurityPolicy,omitempty"`
}

type AddressPair struct {
	// ipAddress is the IP address of the allowed address pair. Depending on
	// the configuration of Neutron, it may be supported to specify a CIDR
	// instead of a specific IP address.
	//
	// +required
	// +kubebuilder:validation:MaxLength=255
	IPAddress string `json:"ipAddress"`
}
