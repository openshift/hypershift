package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func init() {
	SchemeBuilder.Register(func(scheme *runtime.Scheme) error {
		scheme.AddKnownTypes(SchemeGroupVersion,
			&AWSEndpointService{},
			&AWSEndpointServiceList{},
		)
		return nil
	})
}

// The following are reasons for the IgnitionEndpointAvailable condition.
const (
	// AWSEndpointServiceAvailable indicates whether the AWS Endpoint Service
	// has been created for the specified NLB in the management VPC
	AWSEndpointServiceAvailable ConditionType = "AWSEndpointServiceAvailable"

	// AWSEndpointServiceAvailable indicates whether the AWS Endpoint has been
	// created in the guest VPC
	AWSEndpointAvailable ConditionType = "AWSEndpointAvailable"

	AWSSuccessReason string = "AWSSuccess"
	AWSErrorReason   string = "AWSError"
)

// AWSEndpointServiceSpec defines the desired state of AWSEndpointService
type AWSEndpointServiceSpec struct {
	// networkLoadBalancerName is the name of the NLB for which an Endpoint Service should be configured
	// +kubebuilder:validation:MaxLength=255
	// NetworkLoadBalancerName is the name of the network load balancer.
	// +required
	NetworkLoadBalancerName string `json:"networkLoadBalancerName"`

	// subnetIDs is the list of subnet IDs to which guest nodes can attach
	// +optional
	// +kubebuilder:validation:MaxItems=10
	// +kubebuilder:validation:items:MaxLength=63
	SubnetIDs []string `json:"subnetIDs,omitempty"`

	// resourceTags is the list of tags to apply to the EndpointService
	// +optional
	// +kubebuilder:validation:MaxItems=25
	ResourceTags []AWSResourceTag `json:"resourceTags,omitempty"`
}

// AWSEndpointServiceStatus defines the observed state of AWSEndpointService
type AWSEndpointServiceStatus struct {
	// conditions contains details for the current state of the Endpoint Service
	// request If there is an error processing the request e.g. the NLB doesn't
	// exist, then the Available condition will be false, reason AWSErrorReason,
	// and the error reported in the message.
	//
	// Current condition types are: "Available"
	// +optional
	// +listType=map
	// +listMapKey=type
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +kubebuilder:validation:MaxItems=10
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// endpointServiceName is the name of the Endpoint Service created in the
	// management VPC
	// +optional
	// +kubebuilder:validation:MaxLength=255
	EndpointServiceName string `json:"endpointServiceName,omitempty"`

	// endpointID is the ID of the Endpoint created in the guest VPC
	// +optional
	// +kubebuilder:validation:MaxLength=255
	EndpointID string `json:"endpointID,omitempty"`

	// dnsNames are the names for the records created in the hypershift private zone
	// +optional
	// +kubebuilder:validation:MaxItems=10
	// +kubebuilder:validation:items:MaxLength=253
	DNSNames []string `json:"dnsNames,omitempty"`

	// dnsZoneID is ID for the hypershift private zone
	// +optional
	// +kubebuilder:validation:MaxLength=255
	DNSZoneID string `json:"dnsZoneID,omitempty"`

	// securityGroupID is the ID for the VPC endpoint SecurityGroup
	// +kubebuilder:validation:MaxLength=255
	// SecurityGroupID is the ID of the security group.
	// +optional
	SecurityGroupID string `json:"securityGroupID,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=awsendpointservices,scope=Namespaced
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// AWSEndpointService specifies a request for an Endpoint Service in AWS
type AWSEndpointService struct {
	metav1.TypeMeta `json:",inline"`
	// metadata is the metadata for the AWSEndpointService.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// spec is the specification for the AWSEndpointService.
	// +optional
	Spec AWSEndpointServiceSpec `json:"spec,omitempty"`
	// status is the status of the AWSEndpointService.
	// +optional
	Status AWSEndpointServiceStatus `json:"status,omitempty"`
}

// AWSEndpointServiceList contains a list of AWSEndpointService
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type AWSEndpointServiceList struct {
	metav1.TypeMeta `json:",inline"`
	// metadata is standard list metadata.
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`
	// items is a list of AWSEndpointService.
	// +kubebuilder:validation:MaxItems=100
	// items is the list of AWSEndpointServices.
	// +required
	Items []AWSEndpointService `json:"items"`
}
