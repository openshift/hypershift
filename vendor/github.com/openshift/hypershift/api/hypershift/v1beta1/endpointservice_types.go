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
	// The name of the NLB for which an Endpoint Service should be configured
	NetworkLoadBalancerName string `json:"networkLoadBalancerName"`

	// SubnetIDs is the list of subnet IDs to which guest nodes can attach
	// +optional
	SubnetIDs []string `json:"subnetIDs,omitempty"`

	// Tags to apply to the EndpointService
	// +optional
	ResourceTags []AWSResourceTag `json:"resourceTags,omitempty"`
}

// AWSEndpointServiceStatus defines the observed state of AWSEndpointService
type AWSEndpointServiceStatus struct {
	// EndpointServiceName is the name of the Endpoint Service created in the
	// management VPC
	// +optional
	EndpointServiceName string `json:"endpointServiceName,omitempty"`

	// EndpointID is the ID of the Endpoint created in the guest VPC
	// +optional
	EndpointID string `json:"endpointID,omitempty"`

	// DNSName are the names for the records created in the hypershift private zone
	// +optional
	DNSNames []string `json:"dnsNames,omitempty"`

	// DNSZoneID is ID for the hypershift private zone
	// +optional
	DNSZoneID string `json:"dnsZoneID,omitempty"`

	// SecurityGroupID is the ID for the VPC endpoint SecurityGroup
	SecurityGroupID string `json:"securityGroupID,omitempty"`

	// Conditions contains details for the current state of the Endpoint Service
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
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:path=awsendpointservices,scope=Namespaced
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// AWSEndpointService specifies a request for an Endpoint Service in AWS
type AWSEndpointService struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   AWSEndpointServiceSpec   `json:"spec,omitempty"`
	Status AWSEndpointServiceStatus `json:"status,omitempty"`
}

// AWSEndpointServiceList contains a list of AWSEndpointService
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type AWSEndpointServiceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AWSEndpointService `json:"items"`
}
