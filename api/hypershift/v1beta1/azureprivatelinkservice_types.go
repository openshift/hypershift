package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func init() {
	SchemeBuilder.Register(func(scheme *runtime.Scheme) error {
		scheme.AddKnownTypes(SchemeGroupVersion,
			&AzurePrivateLinkService{},
			&AzurePrivateLinkServiceList{},
		)
		return nil
	})
}

// The following are condition types and reasons for AzurePrivateLinkService.
const (
	// AzurePrivateLinkServiceAvailable indicates overall PLS infrastructure availability
	AzurePrivateLinkServiceAvailable ConditionType = "AzurePrivateLinkServiceAvailable"

	// AzureInternalLoadBalancerAvailable indicates the ILB has been provisioned with a frontend IP
	AzureInternalLoadBalancerAvailable ConditionType = "AzureInternalLoadBalancerAvailable"

	// AzurePLSCreated indicates the Azure Private Link Service has been created in the management cluster resource group
	AzurePLSCreated ConditionType = "AzurePLSCreated"

	// AzurePrivateEndpointAvailable indicates the Private Endpoint has been created in the guest VNet
	AzurePrivateEndpointAvailable ConditionType = "AzurePrivateEndpointAvailable"

	// AzurePrivateDNSAvailable indicates the Private DNS zone and A records have been created
	AzurePrivateDNSAvailable ConditionType = "AzurePrivateDNSAvailable"

	AzurePLSSuccessReason string = "AzureSuccess"
	AzurePLSErrorReason   string = "AzureError"
)

// AzureSubnetResourceID is a full Azure resource ID for a subnet.
// The expected format is:
//
//	/subscriptions/{subscriptionID}/resourceGroups/{resourceGroup}/providers/Microsoft.Network/virtualNetworks/{vnetName}/subnets/{subnetName}
//
// +kubebuilder:validation:MinLength=1
// +kubebuilder:validation:MaxLength=355
// +kubebuilder:validation:XValidation:rule="self.matches('^/subscriptions/[^/]+/resourceGroups/[^/]+/providers/Microsoft\\\\.Network/virtualNetworks/[^/]+/subnets/[^/]+$')",message="must be a valid Azure subnet resource ID (e.g., /subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.Network/virtualNetworks/{vnet}/subnets/{subnet})"
type AzureSubnetResourceID string

// AzureVNetResourceID is a full Azure resource ID for a virtual network.
// The expected format is:
//
//	/subscriptions/{subscriptionID}/resourceGroups/{resourceGroup}/providers/Microsoft.Network/virtualNetworks/{vnetName}
//
// +kubebuilder:validation:MinLength=1
// +kubebuilder:validation:MaxLength=355
// +kubebuilder:validation:XValidation:rule="self.matches('^/subscriptions/[^/]+/resourceGroups/[^/]+/providers/Microsoft\\\\.Network/virtualNetworks/[^/]+$')",message="must be a valid Azure VNet resource ID (e.g., /subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.Network/virtualNetworks/{vnet})"
type AzureVNetResourceID string

// AzureSubscriptionID is an Azure subscription ID in UUID format.
// Must be exactly 36 characters consisting of hexadecimal digits [0-9a-fA-F] and hyphens
// in the format xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx (e.g., "550e8400-e29b-41d4-a716-446655440000").
//
// +kubebuilder:validation:MinLength=36
// +kubebuilder:validation:MaxLength=36
// +kubebuilder:validation:XValidation:rule="self.matches('^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$')",message="must be a valid UUID (e.g., 550e8400-e29b-41d4-a716-446655440000)"
type AzureSubscriptionID string

// AzurePrivateLinkServiceSpec defines the desired state of AzurePrivateLinkService
type AzurePrivateLinkServiceSpec struct {
	// loadBalancerIP is the frontend IP address of the internal load balancer that
	// fronts the hosted control plane's API server. This field is populated automatically
	// by the control plane operator from the kube-apiserver service status.
	// It is not set by users directly.
	// When set, the value must be a valid IPv4 or IPv6 address.
	//
	// +optional
	// +kubebuilder:validation:XValidation:rule="isIP(self)",message="loadBalancerIP must be a valid IPv4 or IPv6 address"
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=39
	LoadBalancerIP string `json:"loadBalancerIP,omitempty"`

	// subscriptionID is the Azure subscription ID where the Private Link Service
	// resources will be created. Must be a valid UUID consisting of hexadecimal
	// characters and hyphens in the format xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx
	// where x is a hexadecimal digit [0-9a-f] (e.g., "550e8400-e29b-41d4-a716-446655440000").
	//
	// +required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="subscriptionID is immutable once set"
	SubscriptionID AzureSubscriptionID `json:"subscriptionID,omitempty"`

	// resourceGroupName is the name of the Azure resource group where the Private Link
	// Service resources will be created. Must be 1-90 characters consisting of
	// alphanumerics, underscores, hyphens, periods, and parentheses. Cannot end with a period.
	// See https://learn.microsoft.com/en-us/azure/azure-resource-manager/management/resource-name-rules
	//
	// +required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="resourceGroupName is immutable once set"
	// +kubebuilder:validation:XValidation:rule="self.matches(r'^[-a-zA-Z0-9_.()]+$') && !self.endsWith('.')",message="resourceGroupName must contain only alphanumerics, underscores, hyphens, periods, and parentheses, and cannot end with a period"
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=90
	ResourceGroupName string `json:"resourceGroupName,omitempty"`

	// location is the Azure region where the Private Link Service resources will be
	// created (e.g., "eastus", "westeurope", "centralus"). Must match the region
	// of the management cluster.
	//
	// +required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="location is immutable once set"
	// +kubebuilder:validation:XValidation:rule="self.matches('^[a-z0-9]+$')",message="location must contain only lowercase alphanumeric characters (e.g., eastus, westeurope, centralus)"
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=50
	Location string `json:"location,omitempty"`

	// natSubnetID is the full Azure resource ID of the subnet used for Private Link Service
	// NAT IP allocation. This subnet must have privateLinkServiceNetworkPolicies disabled.
	// If not provided, the controller will auto-create a NAT subnet in the HC's VNet.
	// The expected format is:
	//   /subscriptions/{subscriptionID}/resourceGroups/{resourceGroup}/providers/Microsoft.Network/virtualNetworks/{vnetName}/subnets/{subnetName}
	//
	// +optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="natSubnetID is immutable once set"
	NATSubnetID AzureSubnetResourceID `json:"natSubnetID,omitempty"`

	// additionalAllowedSubscriptions is an optional list of additional Azure subscription IDs
	// permitted to create Private Endpoints to the Private Link Service. The guest cluster's
	// own subscription (derived from guestSubnetID) is always automatically allowed, so it
	// does not need to be listed here.
	// Each entry must be a valid UUID of exactly 36 characters consisting of
	// lowercase hexadecimal characters and hyphens in the format
	// xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx where x is a hexadecimal digit [0-9a-f]
	// (e.g., "550e8400-e29b-41d4-a716-446655440000").
	// A maximum of 50 subscriptions may be specified.
	//
	// +optional
	// +listType=set
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=50
	AdditionalAllowedSubscriptions []AzureSubscriptionID `json:"additionalAllowedSubscriptions,omitempty"`

	// guestSubnetID is the full Azure resource ID of the subnet in the guest VNet where
	// the Private Endpoint will be created.
	// The expected format is:
	//   /subscriptions/{subscriptionID}/resourceGroups/{resourceGroup}/providers/Microsoft.Network/virtualNetworks/{vnetName}/subnets/{subnetName}
	//
	// +required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="guestSubnetID is immutable once set"
	GuestSubnetID AzureSubnetResourceID `json:"guestSubnetID,omitempty"`

	// guestVNetID is the full Azure resource ID of the guest VNet for Private DNS zone linking.
	// The expected format is:
	//   /subscriptions/{subscriptionID}/resourceGroups/{resourceGroup}/providers/Microsoft.Network/virtualNetworks/{vnetName}
	//
	// +required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="guestVNetID is immutable once set"
	GuestVNetID AzureVNetResourceID `json:"guestVNetID,omitempty"`

	// baseDomain is the cluster's base domain (e.g., "example.hypershift.azure.devcluster.openshift.com").
	// Used to create a Private DNS Zone so that worker VMs can resolve the API and OAuth
	// hostnames (api-<name>.<baseDomain>, oauth-<name>.<baseDomain>) to the Private Endpoint IP.
	// Persisted in spec so that deletion does not depend on the HostedControlPlane still existing.
	// baseDomain must be at most 253 characters in length and must consist only of
	// lowercase alphanumeric characters, hyphens, and periods. Each period-separated segment
	// must start and end with an alphanumeric character.
	//
	// +optional
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="baseDomain is immutable once set"
	// +kubebuilder:validation:XValidation:rule="self.matches('^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?(\\\\.[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?)*$')",message="baseDomain must be a valid DNS domain name consisting of alphanumeric characters, hyphens, and periods, where each segment starts and ends with an alphanumeric character"
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	BaseDomain string `json:"baseDomain,omitempty"`
}

// AzurePrivateLinkServiceStatus defines the observed state of AzurePrivateLinkService
// +kubebuilder:validation:MinProperties=1
type AzurePrivateLinkServiceStatus struct {
	// conditions represent the current state of PLS infrastructure.
	// Current condition types are: "AzurePrivateLinkServiceAvailable", "AzureInternalLoadBalancerAvailable",
	// "AzurePLSCreated", "AzurePrivateEndpointAvailable", "AzurePrivateDNSAvailable"
	//
	// +optional
	// +listType=map
	// +listMapKey=type
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=10
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// internalLoadBalancerID is the Azure resource ID of the internal load balancer
	// fronting the hosted control plane. The expected format is:
	//   /subscriptions/{subscriptionID}/resourceGroups/{resourceGroup}/providers/Microsoft.Network/loadBalancers/{loadBalancerName}
	// where subscriptionID is a UUID, resourceGroup is up to 90 characters, and
	// loadBalancerName is up to 80 characters.
	//
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=284
	// +kubebuilder:validation:XValidation:rule="self.matches('^/subscriptions/[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}/resourceGroups/[^/]+/providers/Microsoft\\\\.Network/loadBalancers/[^/]+$')",message="internalLoadBalancerID must be an Azure load balancer resource ID in the format /subscriptions/{subscriptionID}/resourceGroups/{resourceGroup}/providers/Microsoft.Network/loadBalancers/{loadBalancerName}"
	InternalLoadBalancerID string `json:"internalLoadBalancerID,omitempty"`

	// privateLinkServiceID is the Azure resource ID of the Private Link Service.
	// The expected format is:
	//   /subscriptions/{subscriptionID}/resourceGroups/{resourceGroup}/providers/Microsoft.Network/privateLinkServices/{plsName}
	//
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=270
	// +kubebuilder:validation:XValidation:rule="self.matches('^/subscriptions/[^/]+/resourceGroups/[^/]+/providers/Microsoft\\\\.Network/privateLinkServices/[^/]+$')",message="privateLinkServiceID must be an Azure Private Link Service resource ID (e.g., /subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.Network/privateLinkServices/{plsName})"
	PrivateLinkServiceID string `json:"privateLinkServiceID,omitempty"`

	// privateLinkServiceAlias is the globally unique alias for the Private Link Service,
	// auto-generated by Azure in the format {plsName}.{guid}.{region}.azure.privatelinkservice.
	// MaxLength=170 covers: PLS name (80) + GUID (36) + region (19, e.g. "southcentralusstage")
	// + "azure.privatelinkservice" (24) + 4 dots + 7 chars headroom.
	//
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=170
	// +kubebuilder:validation:XValidation:rule="self.matches('^[^.]+\\\\.[0-9a-fA-F-]+\\\\.[a-z0-9]+\\\\.azure\\\\.privatelinkservice$')",message="privateLinkServiceAlias must match the Azure PLS alias format {plsName}.{guid}.{region}.azure.privatelinkservice"
	PrivateLinkServiceAlias string `json:"privateLinkServiceAlias,omitempty"`

	// privateEndpointID is the Azure resource ID of the Private Endpoint.
	// The expected format is:
	//   /subscriptions/{subscriptionID}/resourceGroups/{resourceGroup}/providers/Microsoft.Network/privateEndpoints/{endpointName}
	//
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=267
	// +kubebuilder:validation:XValidation:rule="self.matches('^/subscriptions/[^/]+/resourceGroups/[^/]+/providers/Microsoft\\\\.Network/privateEndpoints/[^/]+$')",message="privateEndpointID must be an Azure Private Endpoint resource ID (e.g., /subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.Network/privateEndpoints/{endpointName})"
	PrivateEndpointID string `json:"privateEndpointID,omitempty"`

	// privateEndpointIP is the private IP address assigned to the Private Endpoint.
	// Must be a valid IPv4 (e.g., "10.0.1.4") or IPv6 address.
	//
	// +optional
	// +kubebuilder:validation:XValidation:rule="isIP(self)",message="privateEndpointIP must be a valid IPv4 or IPv6 address"
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=39
	PrivateEndpointIP string `json:"privateEndpointIP,omitempty"`

	// privateDNSZoneID is the Azure resource ID of the Private DNS Zone.
	// The expected format is:
	//   /subscriptions/{subscriptionID}/resourceGroups/{resourceGroup}/providers/Microsoft.Network/privateDnsZones/{zoneName}
	//
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=265
	// +kubebuilder:validation:XValidation:rule="self.matches('^/subscriptions/[^/]+/resourceGroups/[^/]+/providers/Microsoft\\\\.Network/privateDnsZones/[^/]+$')",message="privateDNSZoneID must be an Azure Private DNS Zone resource ID (e.g., /subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.Network/privateDnsZones/{zoneName})"
	PrivateDNSZoneID string `json:"privateDNSZoneID,omitempty"`

	// dnsZoneName is the Private DNS zone name (derived from the KAS hostname).
	// Persisted at creation time so that deletion does not depend on the
	// HostedControlPlane still existing.
	// Must be a valid DNS domain name consisting of alphanumeric characters, hyphens,
	// and periods, where each segment starts and ends with an alphanumeric character
	// (e.g., "api-mycluster.example.hypershift.azure.devcluster.openshift.com").
	//
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:XValidation:rule="self.matches('^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?(\\\\.[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?)*$')",message="dnsZoneName must be a valid DNS domain name consisting of alphanumeric characters, hyphens, and periods, where each segment starts and ends with an alphanumeric character"
	DNSZoneName string `json:"dnsZoneName,omitempty"`

	// baseDomainDNSZoneID is the Azure resource ID of the base domain Private DNS Zone.
	// The expected format is:
	//   /subscriptions/{subscriptionID}/resourceGroups/{resourceGroup}/providers/Microsoft.Network/privateDnsZones/{zoneName}
	//
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=265
	// +kubebuilder:validation:XValidation:rule="self.matches('^/subscriptions/[^/]+/resourceGroups/[^/]+/providers/Microsoft\\\\.Network/privateDnsZones/[^/]+$')",message="baseDomainDNSZoneID must be an Azure Private DNS Zone resource ID (e.g., /subscriptions/{sub}/resourceGroups/{rg}/providers/Microsoft.Network/privateDnsZones/{zoneName})"
	BaseDomainDNSZoneID string `json:"baseDomainDNSZoneID,omitempty"`
}

// +genclient
// +kubebuilder:object:root=true
// +kubebuilder:resource:path=azureprivatelinkservices,scope=Namespaced,shortName=azpls
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="PLS Alias",type="string",JSONPath=".status.privateLinkServiceAlias",description="Globally unique PLS alias"
// +kubebuilder:printcolumn:name="Endpoint IP",type="string",JSONPath=".status.privateEndpointIP",description="IP address of the Private Endpoint"
// +kubebuilder:printcolumn:name="Available",type="string",JSONPath=".status.conditions[?(@.type==\"AzurePrivateLinkServiceAvailable\")].status",description="PLS availability status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

// AzurePrivateLinkService represents Azure Private Link Service infrastructure
// for private connectivity to hosted cluster API servers.
type AzurePrivateLinkService struct {
	metav1.TypeMeta `json:",inline"`
	// metadata is the metadata for the AzurePrivateLinkService.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// spec is the specification for the AzurePrivateLinkService.
	// +required
	Spec AzurePrivateLinkServiceSpec `json:"spec,omitzero"`
	// status is the status of the AzurePrivateLinkService.
	// +optional
	Status AzurePrivateLinkServiceStatus `json:"status,omitzero"`
}

// AzurePrivateLinkServiceList contains a list of AzurePrivateLinkService
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type AzurePrivateLinkServiceList struct {
	metav1.TypeMeta `json:",inline"`
	// metadata is standard list metadata.
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`
	// items is a list of AzurePrivateLinkService.
	// +kubebuilder:validation:MaxItems=100
	// +required
	Items []AzurePrivateLinkService `json:"items"`
}
