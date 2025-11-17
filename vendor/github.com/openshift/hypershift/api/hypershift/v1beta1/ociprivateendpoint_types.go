package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func init() {
	SchemeBuilder.Register(func(scheme *runtime.Scheme) error {
		scheme.AddKnownTypes(SchemeGroupVersion,
			&OCIPrivateEndpoint{},
			&OCIPrivateEndpointList{},
		)
		return nil
	})
}

// The following are reasons and condition types for OCI Private Endpoint.
const (
	// OCIPrivateEndpointAvailable indicates overall private endpoint infrastructure availability
	OCIPrivateEndpointAvailable ConditionType = "OCIPrivateEndpointAvailable"

	// OCINetworkLoadBalancerReady indicates whether the Network Load Balancer
	// has been created and configured in the management VCN
	OCINetworkLoadBalancerReady ConditionType = "OCINetworkLoadBalancerReady"

	// OCIServiceGatewayAttached indicates whether the Service Gateway has been
	// configured in the customer VCN
	OCIServiceGatewayAttached ConditionType = "OCIServiceGatewayAttached"

	// OCIRouteRulesConfigured indicates whether route table rules have been
	// created to direct traffic to the Service Gateway
	OCIRouteRulesConfigured ConditionType = "OCIRouteRulesConfigured"

	// OCIDNSConfigured indicates whether the private DNS zone and A records
	// have been created in the customer VCN
	OCIDNSConfigured ConditionType = "OCIDNSConfigured"

	OCISuccessReason string = "OCISuccess"
	OCIErrorReason   string = "OCIError"
)

// OCIPrivateEndpointSpec defines the desired state of OCI Private Endpoint infrastructure
type OCIPrivateEndpointSpec struct {
	// networkLoadBalancerID is the OCID of the Network Load Balancer in the management VCN
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=255
	// +kubebuilder:validation:Pattern=`^ocid1\.loadbalancer\.oc[0-9]+\.[a-z0-9.-]*\.[a-z0-9]+$`
	NetworkLoadBalancerID string `json:"networkLoadBalancerID"`

	// reservedIP is the reserved private IP address for the Network Load Balancer
	// +optional
	// +kubebuilder:validation:MaxLength=15
	// +kubebuilder:validation:Pattern=`^((25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\.){3}(25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)$`
	ReservedIP string `json:"reservedIP,omitempty"`

	// serviceGateway is the reference to the Service Gateway in the customer VCN
	// +optional
	ServiceGateway *OCIResourceReference `json:"serviceGateway,omitempty"`

	// customerVCN is the reference to the customer VCN
	// +required
	CustomerVCN OCIResourceReference `json:"customerVCN"`

	// allowedCIDRs specifies which CIDR blocks are allowed to access the private endpoint
	// +optional
	// +kubebuilder:validation:MaxItems=50
	// +kubebuilder:validation:items:MaxLength=43
	// +kubebuilder:validation:items:Pattern=`^((25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\.){3}(25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\/(3[0-2]|[12]?[0-9])$`
	AllowedCIDRs []string `json:"allowedCIDRs,omitempty"`
}

// OCIPrivateEndpointStatus defines the observed state of OCI Private Endpoint infrastructure
type OCIPrivateEndpointStatus struct {
	// conditions represent the current state of private endpoint infrastructure
	// Current condition types are: "OCIPrivateEndpointAvailable", "OCINetworkLoadBalancerReady", "OCIServiceGatewayAttached", "OCIRouteRulesConfigured", "OCIDNSConfigured"
	// +optional
	// +listType=map
	// +listMapKey=type
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +kubebuilder:validation:MaxItems=10
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Management Side Status (Network Load Balancer)

	// networkLoadBalancerIP is the private IP address assigned to the Network Load Balancer
	// +optional
	// +kubebuilder:validation:MaxLength=15
	// +kubebuilder:validation:Pattern=`^((25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\.){3}(25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)$`
	NetworkLoadBalancerIP string `json:"networkLoadBalancerIP,omitempty"`

	// Customer Side Status (Service Gateway, Route Rules, DNS)

	// serviceGatewayID is the OCID of the Service Gateway in the customer VCN
	// +optional
	// +kubebuilder:validation:MaxLength=255
	ServiceGatewayID string `json:"serviceGatewayID,omitempty"`

	// routeTableID is the OCID of the route table with rules directing traffic to the Service Gateway
	// +optional
	// +kubebuilder:validation:MaxLength=255
	RouteTableID string `json:"routeTableID,omitempty"`

	// dnsZoneID is the OCID of the private DNS zone in the customer VCN
	// +optional
	// +kubebuilder:validation:MaxLength=255
	DNSZoneID string `json:"dnsZoneID,omitempty"`

	// dnsRecordName is the A record pointing to the Network Load Balancer private IP
	// +optional
	// +kubebuilder:validation:MaxLength=253
	DNSRecordName string `json:"dnsRecordName,omitempty"`
}

// +genclient
// +kubebuilder:object:root=true
// +kubebuilder:resource:path=ociprivateendpoints,scope=Namespaced,shortName=ocipe
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Network Load Balancer IP",type="string",JSONPath=".status.networkLoadBalancerIP",description="IP address of the Network Load Balancer"
// +kubebuilder:printcolumn:name="Service Gateway",type="string",JSONPath=".status.serviceGatewayID",description="OCID of the Service Gateway"
// +kubebuilder:printcolumn:name="Available",type="string",JSONPath=".status.conditions[?(@.type==\"OCIPrivateEndpointAvailable\")].status",description="Overall private endpoint availability status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +openshift:enable:FeatureGate=OCIPlatform

// OCIPrivateEndpoint represents OCI Private Endpoint infrastructure for private connectivity
// between customer VCNs and hosted control planes.
// This resource is feature-gated behind the OCIPlatform feature gate.
type OCIPrivateEndpoint struct {
	metav1.TypeMeta `json:",inline"`
	// metadata is the metadata for the OCIPrivateEndpoint.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// spec is the specification for the OCIPrivateEndpoint.
	// +optional
	Spec OCIPrivateEndpointSpec `json:"spec,omitempty"`
	// status is the status of the OCIPrivateEndpoint.
	// +optional
	Status OCIPrivateEndpointStatus `json:"status,omitempty"`
}

// OCIPrivateEndpointList contains a list of OCIPrivateEndpoint
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type OCIPrivateEndpointList struct {
	metav1.TypeMeta `json:",inline"`
	// metadata is standard list metadata.
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`
	// items is a list of OCIPrivateEndpoint.
	// +kubebuilder:validation:MaxItems=100
	// +required
	Items []OCIPrivateEndpoint `json:"items"`
}
