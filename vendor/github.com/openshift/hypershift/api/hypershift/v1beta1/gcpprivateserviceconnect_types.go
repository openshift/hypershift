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
	// GCPPrivateServiceConnectReady indicates overall PSC infrastructure readiness
	GCPPrivateServiceConnectReady ConditionType = "GCPPrivateServiceConnectReady"

	// GCPServiceAttachmentReady indicates whether the GCP Service Attachment
	// has been created for the specified Internal Load Balancer in the management VPC
	GCPServiceAttachmentReady ConditionType = "GCPServiceAttachmentReady"

	// GCPEndpointReady indicates whether the GCP PSC Endpoint has been
	// created in the customer VPC
	GCPEndpointReady ConditionType = "GCPEndpointReady"

	// GCPDNSReady indicates whether the DNS configuration has been
	// created in the customer VPC
	GCPDNSReady ConditionType = "GCPDNSReady"

	GCPSuccessReason string = "GCPSuccess"
	GCPErrorReason   string = "GCPError"
)

// GCPPrivateServiceConnectSpec defines the desired state of PSC infrastructure
type GCPPrivateServiceConnectSpec struct {
	// forwardingRuleName is the name of the Internal Load Balancer forwarding rule
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z]([-a-z0-9]*[a-z0-9])?$`
	ForwardingRuleName string `json:"forwardingRuleName"`

	// consumerAcceptList specifies which customer projects can connect
	// +required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=50
	// +kubebuilder:validation:items:MaxLength=30
	// +kubebuilder:validation:items:Pattern=`^[a-z][a-z0-9-]{4,28}[a-z0-9]$`
	ConsumerAcceptList []string `json:"consumerAcceptList"`

	// natSubnet is the subnet used for NAT by the Service Attachment
	// Auto-populated by the Hypershift Operator (implementation out of scope)
	// +optional
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z]([-a-z0-9]*[a-z0-9])?$`
	NATSubnet string `json:"natSubnet,omitempty"`
}

// GCPPrivateServiceConnectStatus defines the observed state of PSC infrastructure
type GCPPrivateServiceConnectStatus struct {
	// conditions represent the current state of PSC infrastructure
	// Current condition types are: "GCPPrivateServiceConnectReady", "GCPServiceAttachmentReady", "GCPEndpointReady", "GCPDNSReady"
	// +optional
	// +listType=map
	// +listMapKey=type
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +kubebuilder:validation:MaxItems=10
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// Management Side Status (Service Attachment)

	// serviceAttachmentName is the name of the created Service Attachment
	// +optional
	// +kubebuilder:validation:MaxLength=63
	ServiceAttachmentName string `json:"serviceAttachmentName,omitempty"`

	// serviceAttachmentURI is the URI customers use to connect
	// Format: projects/{project}/regions/{region}/serviceAttachments/{name}
	// +optional
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:Pattern=`^projects/[a-z][a-z0-9-]{4,28}[a-z0-9]/regions/[a-z]+-[a-z0-9]+[0-9]/serviceAttachments/[a-z]([-a-z0-9]*[a-z0-9])?$`
	ServiceAttachmentURI string `json:"serviceAttachmentURI,omitempty"`

	// Customer Side Status (PSC Endpoint and DNS)

	// endpointIP is the reserved IP address for the PSC endpoint
	// +optional
	// +kubebuilder:validation:Pattern=`^((25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\.){3}(25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)$`
	EndpointIP string `json:"endpointIP,omitempty"`

	// dnsZoneName is the private DNS zone name
	// +optional
	// +kubebuilder:validation:MaxLength=253
	DNSZoneName string `json:"dnsZoneName,omitempty"`

	// dnsRecords lists the created DNS A records
	// +optional
	// +kubebuilder:validation:MaxItems=10
	// +kubebuilder:validation:items:MaxLength=253
	DNSRecords []string `json:"dnsRecords,omitempty"`
}

// +genclient
// +kubebuilder:object:root=true
// +kubebuilder:resource:path=gcpprivateserviceconnects,scope=Namespaced,shortName=gcppsc
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Service Attachment",type="string",JSONPath=".status.serviceAttachmentName",description="Name of the Service Attachment"
// +kubebuilder:printcolumn:name="Endpoint IP",type="string",JSONPath=".status.endpointIP",description="IP address of the PSC endpoint"
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type==\"GCPPrivateServiceConnectReady\")].status",description="Overall PSC readiness status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"
// +openshift:enable:FeatureGate=GCPPlatform

// GCPPrivateServiceConnect represents GCP Private Service Connect infrastructure
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