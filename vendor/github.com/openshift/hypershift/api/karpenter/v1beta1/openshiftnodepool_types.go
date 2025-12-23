package v1beta1

import (
	"github.com/awslabs/operatorpkg/status"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	karpenterv1 "sigs.k8s.io/karpenter/pkg/apis/v1"
)

// OpenshiftNodePoolStatus defines the observed state of OpenshiftNodePool.
type OpenshiftNodePoolStatus struct {
	// Resources is the list of resources that have been provisioned.
	// +optional
	Resources corev1.ResourceList `json:"resources,omitempty"`
	// Conditions contains signals for health and readiness
	// +optional
	Conditions []status.Condition `json:"conditions,omitempty"`
}

// OpenshiftNodePoolSpec defines the desired state of OpenshiftNodePool.
type OpenshiftNodePoolSpec struct {
	// Template contains the template of possibilities for the provisioning logic to launch a NodeClaim with.
	// +required
	Template karpenterv1.NodeClaimTemplate `json:"template"`
	// Disruption contains the parameters that relate to Karpenter's disruption logic
	// +kubebuilder:default:={consolidateAfter: "0s"}
	// +optional
	Disruption karpenterv1.Disruption `json:"disruption,omitempty"`
	// Limits define a set of bounds for provisioning capacity.
	// +optional
	Limits karpenterv1.Limits `json:"limits,omitempty"`
	// Weight is the priority given to the nodepool during scheduling. A higher
	// numerical weight indicates that this nodepool should be considered first in scheduling.
	// +kubebuilder:validation:Minimum:=1
	// +kubebuilder:validation:Maximum:=100
	// +optional
	Weight *int32 `json:"weight,omitempty"`
}

// +genclient
// +kubebuilder:object:root=true
// +kubebuilder:resource:path=openshiftnodepools,shortName=onp;onps,scope=Cluster
// +kubebuilder:printcolumn:name="NodeClass",type="string",JSONPath=".spec.template.spec.nodeClassRef.name",description=""
// +kubebuilder:printcolumn:name="Nodes",type="string",JSONPath=".status.resources.nodes",description=""
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type==\"Ready\")].status",description=""
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp",description=""
// +kubebuilder:printcolumn:name="Weight",type="integer",JSONPath=".spec.weight",priority=1,description=""
// +kubebuilder:printcolumn:name="CPU",type="string",JSONPath=".status.resources.cpu",priority=1,description=""
// +kubebuilder:printcolumn:name="Memory",type="string",JSONPath=".status.resources.memory",priority=1,description=""
// +kubebuilder:subresource:status
// +kubebuilder:storageversion
// OpenshiftNodePool defines the desired state of OpenshiftNodePool.
type OpenshiftNodePool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// +required
	Spec   OpenshiftNodePoolSpec   `json:"spec"`
	Status OpenshiftNodePoolStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
// OpenshiftNodePoolList contains a list of OpenshiftNodePool.
type OpenshiftNodePoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []OpenshiftNodePool `json:"items"`
}
