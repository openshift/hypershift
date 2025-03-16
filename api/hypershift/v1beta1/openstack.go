package v1beta1

import "fmt"

// PortSecurityPolicy defines whether or not to enable port security on a port.
type PortSecurityPolicy string

const (
	// PortSecurityEnabled enables port security on a port.
	PortSecurityEnabled PortSecurityPolicy = "Enabled"

	// PortSecurityDisabled disables port security on a port.
	PortSecurityDisabled PortSecurityPolicy = "Disabled"

	// PortSecurityDefault uses the default port security policy.
	PortSecurityDefault PortSecurityPolicy = ""

	// PruneRetentionPolicy is the default policy for handling OpenStack Glance Images
	// when the HostedCluster is deleted.
	DefaultImageRetentionPolicy RetentionPolicy = OrphanRetentionPolicy
)

func (p *RetentionPolicy) String() string {
	return string(*p)
}

func (p *RetentionPolicy) Set(s string) error {
	switch s {
	case string(OrphanRetentionPolicy):
		*p = OrphanRetentionPolicy
	case string(PruneRetentionPolicy):
		*p = PruneRetentionPolicy
	default:
		return fmt.Errorf("invalid RetentionPolicy: %s", s)
	}
	return nil
}

func (p *RetentionPolicy) Type() string {
	return "RetentionPolicy"
}

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

	// AdditionalPorts is a list of additional ports to create on the node instances.
	//
	// +optional
	AdditionalPorts []PortSpec `json:"additionalPorts,omitempty"`
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

	// IngressFloatingIP is an available floating IP in your OpenStack cluster that will
	// be associated with the OpenShift ingress port.
	// When not specified, an IP address will be assigned randomly by the OpenStack cloud provider.
	// When specified, the floating IP has to be pre-created.  If the
	// specified value is not a floating IP or is already claimed, the
	// OpenStack cloud provider won't be able to provision the load
	// balancer.
	// This value must be a valid IPv4 or IPv6 address.
	//
	// +kubebuilder:validation:XValidation:rule="isIP(self)",message="floatingIP must be a valid IPv4 or IPv6 address"
	// +optional
	IngressFloatingIP string `json:"ingressFloatingIP,omitempty"`

	// imageRetentionPolicy defines the policy for handling resources associated with the image
	// when the cluster is deleted.
	// The default (set by the Nodepool controller) is to orphan the image so that it can be
	// used by other clusters. If the image is no longer needed, it can be manually deleted.
	// If the image is set to be pruned, it will be deleted when the cluster is deleted.
	// It is defined at the HostedCluster level and will be used for all nodepools images
	// so there is no conflict between different ORC objects.
	// On day 2 operations, if this field is changed, the corresponding ORC object will be updated
	// to reflect the chosen retention policy (prune or orphan) which is translated into ORC format
	// (either 'delete' or 'detach' in ORC terminology).
	//
	// +optional
	ImageRetentionPolicy RetentionPolicy `json:"imageRetentionPolicy,omitempty"`
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

// PortSpec specifies the options for creating a port.
type PortSpec struct {
	// Network is a query for an openstack network that the port will be created or discovered on.
	// This will fail if the query returns more than one network.
	//
	// +optional
	Network *NetworkParam `json:"network,omitempty"`

	// Description is a human-readable description for the port.
	//
	// +optional
	Description string `json:"description,omitempty"`

	// AllowedAddressPairs is a list of address pairs which Neutron will
	// allow the port to send traffic from in addition to the port's
	// addresses. If not specified, the MAC Address will be the MAC Address
	// of the port. Depending on the configuration of Neutron, it may be
	// supported to specify a CIDR instead of a specific IP address.
	//
	// +optional
	AllowedAddressPairs []AddressPair `json:"allowedAddressPairs,omitempty"`

	// VNICType specifies the type of vNIC which this port should be
	// attached to. This is used to determine which mechanism driver(s) to
	// be used to bind the port. The valid values are normal, macvtap,
	// direct, baremetal, direct-physical, virtio-forwarder, smart-nic and
	// remote-managed, although these values will not be validated in this
	// API to ensure compatibility with future neutron changes or custom
	// implementations. What type of vNIC is actually available depends on
	// deployments. If not specified, the Neutron default value is used.
	//
	// +optional
	VNICType string `json:"vnicType,omitempty"`

	// PortSecurityPolicy specifies whether or not to enable port security on the port.
	// Allowed values are "Enabled", "Disabled" and omitted.
	// When not set, it takes the value of the corresponding field at the network level.
	//
	// +kubebuilder:validation:Enum:=Enabled;Disabled;""
	// +optional
	PortSecurityPolicy PortSecurityPolicy `json:"portSecurityPolicy,omitempty"`
}

type AddressPair struct {
	// IPAddress is the IP address of the allowed address pair. Depending on
	// the configuration of Neutron, it may be supported to specify a CIDR
	// instead of a specific IP address.
	//
	// +kubebuilder:validation:Required
	// +required
	IPAddress string `json:"ipAddress"`
}
