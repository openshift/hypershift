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

// AzurePrivateLinkServiceSpec defines the desired state of AzurePrivateLinkService
type AzurePrivateLinkServiceSpec struct {
	// loadBalancerIP is the frontend IP address of the internal load balancer.
	// This field is populated by the CPO observer from the KAS service status,
	// not supplied by users. It is placed in Spec rather than Status because:
	//   - It follows the same pattern as GCP Private Service Connect (GCPPrivateServiceConnect.Spec.LoadBalancerIP)
	//   - It enables the HO controller to consume this value through normal Spec watches
	//     without requiring a status subresource watch
	// This value must be a valid IPv4 or IPv6 address.
	//
	// +optional
	// +kubebuilder:validation:XValidation:rule="self == '' || self.matches('^((\\\\d{1,3}\\\\.){3}\\\\d{1,3})$') || self.matches('^(([0-9a-fA-F]{1,4}:){7}[0-9a-fA-F]{1,4}|([0-9a-fA-F]{1,4}:){1,7}:|([0-9a-fA-F]{1,4}:){1,6}:[0-9a-fA-F]{1,4}|([0-9a-fA-F]{1,4}:){1,5}(:[0-9a-fA-F]{1,4}){1,2}|([0-9a-fA-F]{1,4}:){1,4}(:[0-9a-fA-F]{1,4}){1,3}|([0-9a-fA-F]{1,4}:){1,3}(:[0-9a-fA-F]{1,4}){1,4}|([0-9a-fA-F]{1,4}:){1,2}(:[0-9a-fA-F]{1,4}){1,5}|[0-9a-fA-F]{1,4}:((:[0-9a-fA-F]{1,4}){1,6})|:((:[0-9a-fA-F]{1,4}){1,7}|:))$')",message="loadBalancerIP must be a valid IPv4 or IPv6 address"
	// +kubebuilder:validation:MaxLength=45
	LoadBalancerIP string `json:"loadBalancerIP,omitempty"`

	// subscriptionID is the Azure subscription ID where the PLS will be created.
	//
	// +required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="subscriptionID is immutable once set"
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	// +kubebuilder:validation:Pattern=`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`
	SubscriptionID string `json:"subscriptionID"`

	// resourceGroupName is the Azure resource group where the PLS will be created.
	//
	// +required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="resourceGroupName is immutable once set"
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=90
	ResourceGroupName string `json:"resourceGroupName"`

	// location is the Azure region where the PLS will be created.
	//
	// +required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="location is immutable once set"
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	Location string `json:"location"`

	// natSubnetID is the Azure resource ID of the subnet used for PLS NAT IP allocation.
	//
	// +required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="natSubnetID is immutable once set"
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=355
	// +kubebuilder:validation:Pattern=`^/subscriptions/[^/]+/resourceGroups/[^/]+/providers/Microsoft\.Network/virtualNetworks/[^/]+/subnets/[^/]+$`
	NATSubnetID string `json:"natSubnetID"`

	// allowedSubscriptions is the list of Azure subscription IDs permitted to create Private Endpoints.
	// This field is intentionally mutable (no immutability constraint) to allow operators to
	// grant or revoke Private Endpoint access without recreating the resource. This follows
	// the same pattern as AWS additionalAllowedPrincipals.
	//
	// +required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=50
	// +kubebuilder:validation:items:MaxLength=255
	// +kubebuilder:validation:items:Pattern=`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`
	AllowedSubscriptions []string `json:"allowedSubscriptions"`

	// guestSubnetID is the Azure resource ID of the subnet in the guest VNet where the Private Endpoint will be created.
	//
	// +required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="guestSubnetID is immutable once set"
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=355
	// +kubebuilder:validation:Pattern=`^/subscriptions/[^/]+/resourceGroups/[^/]+/providers/Microsoft\.Network/virtualNetworks/[^/]+/subnets/[^/]+$`
	GuestSubnetID string `json:"guestSubnetID"`

	// guestVNetID is the Azure resource ID of the guest VNet for DNS zone linking.
	//
	// +required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="guestVNetID is immutable once set"
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	// +kubebuilder:validation:Pattern=`^/subscriptions/[^/]+/resourceGroups/[^/]+/providers/Microsoft\.Network/virtualNetworks/[^/]+$`
	GuestVNetID string `json:"guestVNetID"`
}

// AzurePrivateLinkServiceStatus defines the observed state of AzurePrivateLinkService
type AzurePrivateLinkServiceStatus struct {
	// conditions represent the current state of PLS infrastructure.
	// Current condition types are: "AzurePrivateLinkServiceAvailable", "AzureInternalLoadBalancerAvailable",
	// "AzurePLSCreated", "AzurePrivateEndpointAvailable", "AzurePrivateDNSAvailable"
	//
	// +optional
	// +listType=map
	// +listMapKey=type
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +kubebuilder:validation:MaxItems=10
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// internalLoadBalancerID is the Azure resource ID of the internal load balancer.
	//
	// +optional
	// +kubebuilder:validation:MaxLength=512
	InternalLoadBalancerID string `json:"internalLoadBalancerID,omitempty"`

	// privateLinkServiceID is the Azure resource ID of the Private Link Service.
	//
	// +optional
	// +kubebuilder:validation:MaxLength=512
	PrivateLinkServiceID string `json:"privateLinkServiceID,omitempty"`

	// privateLinkServiceAlias is the globally unique alias for the Private Link Service.
	//
	// +optional
	// +kubebuilder:validation:MaxLength=255
	PrivateLinkServiceAlias string `json:"privateLinkServiceAlias,omitempty"`

	// privateEndpointID is the Azure resource ID of the Private Endpoint.
	//
	// +optional
	// +kubebuilder:validation:MaxLength=512
	PrivateEndpointID string `json:"privateEndpointID,omitempty"`

	// privateEndpointIP is the private IP address assigned to the Private Endpoint.
	//
	// +optional
	// +kubebuilder:validation:MaxLength=45
	PrivateEndpointIP string `json:"privateEndpointIP,omitempty"`

	// privateDNSZoneID is the Azure resource ID of the Private DNS Zone.
	//
	// +optional
	// +kubebuilder:validation:MaxLength=512
	PrivateDNSZoneID string `json:"privateDNSZoneID,omitempty"`

	// dnsZoneName is the Private DNS zone name (derived from the KAS hostname).
	// Persisted at creation time so that deletion does not depend on the
	// HostedControlPlane still existing.
	//
	// +optional
	// +kubebuilder:validation:MaxLength=255
	DNSZoneName string `json:"dnsZoneName,omitempty"`
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
	// +optional
	Spec AzurePrivateLinkServiceSpec `json:"spec,omitempty"`
	// status is the status of the AzurePrivateLinkService.
	// +optional
	Status AzurePrivateLinkServiceStatus `json:"status,omitempty"`
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
