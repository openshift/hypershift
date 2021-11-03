package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func init() {
	SchemeBuilder.Register(&AWSEndpointService{}, &AWSEndpointServiceList{})
}

// The following are reasons for the IgnitionEndpointAvailable condition.
const (
	AWSSuccessReason string = "AWSSuccessReason"
	AWSErrorReason   string = "AWSErrorReason"
)

// AWSEndpointServiceSpec defines the desired state of AWSEndpointService
type AWSEndpointServiceSpec struct {
	// The name of the NLB for which an Endpoint Service should be configured
	NetworkLoadBalancerName string `json:"networkLoadBalancerName,omitempty"`
}

// AWSEndpointServiceStatus defines the observed state of AWSEndpointService
type AWSEndpointServiceStatus struct {
	// The endpoint service name created in AWS in response to the request
	// +optional
	EndpointServiceName string `json:"endpointServiceName,omitempty"`

	// Condition contains details for the current state of the Endpoint Service
	// request If there is an error processing the request e.g. the NLB doesn't
	// exist, then the Available condition will be false, reason AWSErrorReason,
	// and the error reported in the message.
	//
	// Current condition types are: "Available"
	Conditions []metav1.Condition `json:"conditions"`
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

// +kubebuilder:object:root=true
// AWSEndpointServiceList contains a list of AWSEndpointService
type AWSEndpointServiceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []AWSEndpointService `json:"items"`
}
