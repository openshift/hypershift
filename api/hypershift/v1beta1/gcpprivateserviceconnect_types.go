package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func init() {
	SchemeBuilder.Register(func(scheme *runtime.Scheme) error {
		scheme.AddKnownTypes(SchemeGroupVersion,
			&GCPPrivateServiceConnect{},
			&GCPPrivateServiceConnectList{},
		)
		return nil
	})
}

// The following are reasons and condition types for GCP Private Service Connect.
const (
	// GCPPrivateServiceConnectAvailable indicates overall PSC infrastructure availability
	GCPPrivateServiceConnectAvailable ConditionType = "GCPPrivateServiceConnectAvailable"

	// GCPServiceAttachmentAvailable indicates whether the GCP Service Attachment
	// has been created for the specified Internal Load Balancer in the management VPC
	GCPServiceAttachmentAvailable ConditionType = "GCPServiceAttachmentAvailable"

	// GCPEndpointAvailable indicates whether the GCP PSC Endpoint has been
	// created in the customer VPC
	GCPEndpointAvailable ConditionType = "GCPEndpointAvailable"

	// GCPDNSAvailable indicates whether the DNS configuration has been
	// created in the customer VPC
	GCPDNSAvailable ConditionType = "GCPDNSAvailable"

	GCPSuccessReason string = "GCPSuccess"
	GCPErrorReason   string = "GCPError"
)

// DNSZoneStatus represents a single DNS zone and its records
type DNSZoneStatus struct {
	// name is the DNS zone name
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name,omitempty"`

	// records lists the DNS records created in this zone
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=10
	// +kubebuilder:validation:items:MaxLength=253
	Records []string `json:"records,omitempty"`
}

// GCPPrivateServiceConnectSpec defines the desired state of PSC infrastructure
type GCPPrivateServiceConnectSpec struct {
	// loadBalancerIP is the IP address of the Internal Load Balancer
	// Populated by the observer from service status
	// This value must be a valid IPv4 or IPv6 address.
	// +required
	// +kubebuilder:validation:XValidation:rule="isIP(self)",message="loadBalancerIP must be a valid IPv4 or IPv6 address"
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=39
	LoadBalancerIP string `json:"loadBalancerIP,omitempty"`

	// forwardingRuleName is the name of the Internal Load Balancer forwarding rule
	// Populated by the reconciler via GCP API lookup
	// +optional
	ForwardingRuleName GCPResourceName `json:"forwardingRuleName,omitempty"`

	// consumerAcceptList specifies which customer projects can connect.
	// Accepts both project IDs (e.g. "my-project-123") and project numbers (e.g. "123456789012").
	// A maximum of 50 entries are allowed.
	// See https://cloud.google.com/resource-manager/docs/creating-managing-projects for project ID and number formats.
	// +required
	// +listType=set
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=50
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=30
	// +kubebuilder:validation:items:XValidation:rule="self.matches('^([a-z][a-z0-9-]{4,28}[a-z0-9]|[0-9]{1,30})$')",message="each entry must be a valid GCP project ID or project number"
	ConsumerAcceptList []string `json:"consumerAcceptList,omitempty"`

	// natSubnet is the subnet used for NAT by the Service Attachment
	// Auto-populated by the HyperShift Operator
	// +optional
	NATSubnet GCPResourceName `json:"natSubnet,omitempty"`
}

// GCPPrivateServiceConnectStatus defines the observed state of PSC infrastructure
type GCPPrivateServiceConnectStatus struct {
	// conditions represent the current state of PSC infrastructure
	// Current condition types are: "GCPPrivateServiceConnectAvailable", "GCPServiceAttachmentAvailable", "GCPEndpointAvailable", "GCPDNSAvailable"
	// +optional
	// +listType=map
	// +listMapKey=type
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=10
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Management Side Status (Service Attachment)

	// serviceAttachmentName is the name of the created Service Attachment
	// +optional
	// +kubebuilder:validation:MaxLength=63
	ServiceAttachmentName string `json:"serviceAttachmentName,omitempty"`

	// serviceAttachmentURI is the URI customers use to connect.
	// Format: projects/{project}/regions/{region}/serviceAttachments/{name}
	// See https://cloud.google.com/vpc/docs/configure-private-service-connect-producer for service attachment details.
	// +optional
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:XValidation:rule="self.matches('^projects/[a-z][a-z0-9-]{4,28}[a-z0-9]/regions/[a-z]+-[a-z]+[0-9]+/serviceAttachments/[a-z]([-a-z0-9]*[a-z0-9])?$')",message="serviceAttachmentURI must be in the format projects/{project}/regions/{region}/serviceAttachments/{name}"
	ServiceAttachmentURI string `json:"serviceAttachmentURI,omitempty"`

	// Customer Side Status (PSC Endpoint and DNS)

	// endpointIP is the reserved IP address for the PSC endpoint
	// This value must be a valid IPv4 or IPv6 address.
	// +optional
	// +kubebuilder:validation:XValidation:rule="isIP(self)",message="endpointIP must be a valid IPv4 or IPv6 address"
	// +kubebuilder:validation:MinLength=3
	// +kubebuilder:validation:MaxLength=39
	EndpointIP string `json:"endpointIP,omitempty"`

	// dnsZones contains DNS zone information created for this cluster
	// +listType=atomic
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=5
	DNSZones []DNSZoneStatus `json:"dnsZones,omitempty"`
}

// +genclient
// +kubebuilder:object:root=true
// +kubebuilder:resource:path=gcpprivateserviceconnects,scope=Namespaced,shortName=gcppsc
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Service Attachment",type="string",JSONPath=".status.serviceAttachmentName",description="Name of the Service Attachment"
// +kubebuilder:printcolumn:name="Endpoint IP",type="string",JSONPath=".status.endpointIP",description="IP address of the PSC endpoint"
// +kubebuilder:printcolumn:name="Available",type="string",JSONPath=".status.conditions[?(@.type==\"GCPEndpointAvailable\")].status",description="PSC endpoint availability status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +openshift:enable:FeatureGate=GCPPlatform

// GCPPrivateServiceConnect represents GCP Private Service Connect infrastructure.
// This resource is feature-gated behind the GCPPlatform feature gate.
type GCPPrivateServiceConnect struct {
	metav1.TypeMeta `json:",inline"`
	// metadata is the metadata for the GCPPrivateServiceConnect.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// spec is the specification for the GCPPrivateServiceConnect.
	// +optional
	Spec GCPPrivateServiceConnectSpec `json:"spec,omitempty"`
	// status is the status of the GCPPrivateServiceConnect.
	// +optional
	Status GCPPrivateServiceConnectStatus `json:"status,omitempty"`
}

// GCPPrivateServiceConnectList contains a list of GCPPrivateServiceConnect
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type GCPPrivateServiceConnectList struct {
	metav1.TypeMeta `json:",inline"`
	// metadata is standard list metadata.
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`
	// items is a list of GCPPrivateServiceConnect.
	// +kubebuilder:validation:MaxItems=100
	// +required
	Items []GCPPrivateServiceConnect `json:"items"`
}
