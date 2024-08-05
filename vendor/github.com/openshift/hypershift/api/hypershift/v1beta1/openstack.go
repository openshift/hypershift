package v1beta1

type OpenStackNodePoolPlatform struct {
	// Flavor is the OpenStack flavor to use for the node instances.
	//
	// +kubebuilder:validation:Required
	// +required
	Flavor string `json:"flavor"`

	// ImageName is the OpenStack Glance image name to use for node instances. If unspecified, the default
	// is chosen based on the NodePool release payload image.
	//
	// +optional
	ImageName string `json:"imageName,omitempty"`
}

// OpenStackPlatformSpec specifies configuration for clusters running on OpenStack.
type OpenStackPlatformSpec struct {
	// IdentityRef is a reference to a secret holding OpenStack credentials
	// to be used when reconciling the hosted cluster.
	//
	// +kubebuilder:validation:Required
	// +required
	IdentityRef OpenStackIdentityReference `json:"identityRef"`

	// ManagedSubnets describe the OpenStack Subnet to be created. Cluster actuator will create a network,
	// and a subnet with the defined DNSNameservers, AllocationPools and the CIDR defined in the HostedCluster
	// MachineNetwork, and a router connected to the subnet. Currently only one IPv4
	// subnet is supported.
	//
	// +kubebuilder:validation:MaxItems=1
	// +listType=atomic
	// +optional
	ManagedSubnets []SubnetSpec `json:"managedSubnets,omitempty"`

	// Router specifies an existing router to be used if ManagedSubnets are
	// specified. If specified, no new router will be created.
	//
	// +optional
	Router *RouterParam `json:"router,omitempty"`

	// Network specifies an existing network to use if no ManagedSubnets
	// are specified.
	// +optional
	Network *NetworkParam `json:"network,omitempty"`

	// Subnets specifies existing subnets to use if not ManagedSubnets are
	// specified. All subnets must be in the network specified by Network.
	// There can be zero, one, or two subnets. If no subnets are specified,
	// all subnets in Network will be used. If 2 subnets are specified, one
	// must be IPv4 and the other IPv6.
	//
	// +kubebuilder:validation:MaxItems=2
	// +listType=atomic
	// +optional
	Subnets []SubnetParam `json:"subnets,omitempty"`

	// NetworkMTU sets the maximum transmission unit (MTU) value to address fragmentation for the private network ID.
	// This value will be used only if the Cluster actuator creates the network.
	// If left empty, the network will have the default MTU defined in Openstack network service.
	// To use this field, the Openstack installation requires the net-mtu neutron API extension.
	//
	// +optional
	NetworkMTU *int `json:"networkMTU,omitempty"`

	// ExternalNetwork is the OpenStack Network to be used to get public internet to the VMs.
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

	// DisableExternalNetwork specifies whether or not to attempt to connect the cluster
	// to an external network. This allows for the creation of clusters when connecting
	// to an external network is not possible or desirable, e.g. if using a provider network.
	//
	// +optional
	DisableExternalNetwork *bool `json:"disableExternalNetwork,omitempty"`

	// Tags to set on all resources in cluster which support tags
	//
	// +listType=set
	// +optional
	Tags []string `json:"tags,omitempty"`
}

// OpenStackIdentityReference is a reference to an infrastructure
// provider identity to be used to provision cluster resources.
type OpenStackIdentityReference struct {
	// Name is the name of a secret in the same namespace as the resource being provisioned.
	// The secret must contain a key named `clouds.yaml` which contains an OpenStack clouds.yaml file.
	// The secret may optionally contain a key named `cacert` containing a PEM-encoded CA certificate.
	//
	// +kubebuilder:validation:Required
	// +required
	Name string `json:"name"`

	// CloudName specifies the name of the entry in the clouds.yaml file to use.
	//
	// +kubebuilder:validation:Required
	// +required
	CloudName string `json:"cloudName"`
}

type SubnetSpec struct {
	// DNSNameservers holds a list of DNS server addresses that will be provided when creating
	// the subnet. These addresses need to have the same IP version as CIDR.
	//
	// +optional
	DNSNameservers []string `json:"dnsNameservers,omitempty"`

	// AllocationPools is an array of AllocationPool objects that will be applied to OpenStack Subnet being created.
	// If set, OpenStack will only allocate these IPs for Machines. It will still be possible to create ports from
	// outside of these ranges manually.
	//
	// +optional
	AllocationPools []AllocationPool `json:"allocationPools,omitempty"`
}

type AllocationPool struct {
	// Start represents the start of the AllocationPool, that is the lowest IP of the pool.
	//
	// +kubebuilder:validation:Required
	// +required
	Start string `json:"start"`

	// End represents the end of the AlloctionPool, that is the highest IP of the pool.
	//
	// +kubebuilder:validation:Required
	// +required
	End string `json:"end"`
}

// RouterParam specifies an OpenStack router to use. It may be specified by either ID or filter, but not both.
// +kubebuilder:validation:MaxProperties:=1
// +kubebuilder:validation:MinProperties:=1
type RouterParam struct {
	// ID is the ID of the router to use. If ID is provided, the other filters cannot be provided. Must be in UUID format.
	//
	// +kubebuilder:validation:Format:=uuid
	// +optional
	ID *string `json:"id,omitempty"`

	// Filter specifies a filter to select an OpenStack router. If provided, cannot be empty.
	//
	// +optional
	Filter *RouterFilter `json:"filter,omitempty"`
}

// RouterFilter specifies a query to select an OpenStack router. At least one property must be set.
// +kubebuilder:validation:MinProperties:=1
type RouterFilter struct {
	// Name is the name of the router to filter by.
	//
	// +optional
	Name string `json:"name,omitempty"`

	// Description is the description of the router to filter by.
	//
	// +optional
	Description string `json:"description,omitempty"`

	// ProjectID is the project ID of the router to filter by.
	//
	// +optional
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
	// ID is the ID of the network to use. If ID is provided, the other filters cannot be provided. Must be in UUID format.
	//
	// +kubebuilder:validation:Format:=uuid
	// +optional
	ID *string `json:"id,omitempty"`

	// Filter specifies a filter to select an OpenStack network. If provided, cannot be empty.
	//
	// +optional
	Filter *NetworkFilter `json:"filter,omitempty"`
}

// NetworkFilter specifies a query to select an OpenStack network. At least one property must be set.
// +kubebuilder:validation:MinProperties:=1
type NetworkFilter struct {
	// Name is the name of the network to filter by.
	//
	// +optional
	Name string `json:"name,omitempty"`

	// Description is the description of the network to filter by.
	//
	// +optional
	Description string `json:"description,omitempty"`

	// ProjectID is the project ID of the network to filter by.
	//
	// +optional
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
type NeutronTag string

type FilterByNeutronTags struct {
	// Tags is a list of tags to filter by. If specified, the resource must
	// have all of the tags specified to be included in the result.
	//
	// +listType=set
	// +optional
	Tags []NeutronTag `json:"tags,omitempty"`

	// TagsAny is a list of tags to filter by. If specified, the resource
	// must have at least one of the tags specified to be included in the
	// result.
	//
	// +listType=set
	// +optional
	TagsAny []NeutronTag `json:"tagsAny,omitempty"`

	// NotTags is a list of tags to filter by. If specified, resources which
	// contain all of the given tags will be excluded from the result.
	//
	// +listType=set
	// +optional
	NotTags []NeutronTag `json:"notTags,omitempty"`

	// NotTagsAny is a list of tags to filter by. If specified, resources
	// which contain any of the given tags will be excluded from the result.
	//
	// +listType=set
	// +optional
	NotTagsAny []NeutronTag `json:"notTagsAny,omitempty"`
}

// SubnetParam specifies an OpenStack subnet to use. It may be specified by either ID or filter, but not both.
// +kubebuilder:validation:MaxProperties:=1
// +kubebuilder:validation:MinProperties:=1
type SubnetParam struct {
	// ID is the uuid of the subnet. It will not be validated.
	//
	// +kubebuilder:validation:Format:=uuid
	// +optional
	ID *string `json:"id,omitempty"`

	// Filter specifies a filter to select the subnet. It must match exactly one subnet.
	//
	// +optional
	Filter *SubnetFilter `json:"filter,omitempty"`
}

// SubnetFilter specifies a filter to select a subnet. At least one parameter must be specified.
// +kubebuilder:validation:MinProperties:=1
type SubnetFilter struct {
	// Name is the name of the subnet to filter by.
	//
	// +optional
	Name string `json:"name,omitempty"`
	// Description is the description of the subnet to filter by.
	//
	// +optional
	Description string `json:"description,omitempty"`

	// ProjectID is the project ID of the subnet to filter by.
	//
	// +optional
	ProjectID string `json:"projectID,omitempty"`

	// IPVersion is the IP version of the subnet to filter by.
	//
	// +optional
	IPVersion int `json:"ipVersion,omitempty"`

	// GatewayIP is the gateway IP of the subnet to filter by.
	//
	// +optional
	GatewayIP string `json:"gatewayIP,omitempty"`

	// CIDR is the CIDR of the subnet to filter by.
	//
	// +optional
	CIDR string `json:"cidr,omitempty"`

	// IPv6AddressMode is the IPv6 address mode of the subnet to filter by.
	//
	// +optional
	IPv6AddressMode string `json:"ipv6AddressMode,omitempty"`

	// IPv6RAMode is the IPv6 RA mode of the subnet to filter by.
	//
	// +optional
	IPv6RAMode string `json:"ipv6RAMode,omitempty"`

	// FilterByNeutronTags specifies tags to filter by.
	//
	// +optional
	FilterByNeutronTags `json:",inline"`
}