package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func init() {
	SchemeBuilder.Register(&WorkloadConfiguration{})
	SchemeBuilder.Register(&WorkloadConfigurationList{})
}

// WorkloadConfiguration allows additional tuning of control plane workloads
// +kubebuilder:resource:path=workloadconfigurations,shortName=wcfg;wcfgs,scope=Namespaced
// +kubebuilder:storageversion
// +kubebuilder:subresource:status
// +kubebuilder:object:root=true
type WorkloadConfiguration struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec contains the workload configuration spec
	// +optional
	Spec WorkloadConfigurationSpec `json:"spec,omitempty"`

	// Status contains the workload configuration status
	// +optional
	Status WorkloadConfigurationStatus `json:"status,omitempty"`
}

type WorkloadConfigurationStatus struct {
}

type WorkloadConfigurationSpec struct {
	// Labels that should be applied to all deployments in the control plane
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// Annotations that should be applied to all deployments in the control plane
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`

	// Deployments contains configuration details for control plane component deployments
	// +listType=map
	// +listMapKey=name
	// +optional
	Deployments []DeploymentWorkloadConfiguration `json:"deployments,omitempty"`
}

// DeploymentWorkloadConfiguration contains configuration specific to a control plane
// deployment.
type DeploymentWorkloadConfiguration struct {
	// Name is the name of the control plane deployment this configuration is for
	Name string `json:"name"`

	// Affinity contains affinity scheduling rules for the deployment
	// +optional
	Affinity *corev1.Affinity `json:"affinity,omitempty"`

	// Tolerations contains tolerations of node taints for the deployment
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`

	// PriorityClassName specifies the priority for pods belonging to the deployment
	// +optional
	PriorityClassName *string `json:"priorityClassName,omitempty"`

	// Labels that should be applied to pods belonging to the deployment
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// Annotations that should be applied to pods belonging to the deployment
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`

	// Containers specifies container-scoped configuration for the deployment
	// +optional
	Containers []ContainerWorkloadConfiguration `json:"containers,omitempty"`
}

// ContainerWorkloadConfiguration contains configuration specific to a container
// inside a control plane deployment
type ContainerWorkloadConfiguration struct {
	// Name is the name of the container that this configuration applies to
	Name string `json:"name"`

	// Resources have resource requirements for the container
	// +optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`

	// SecurityContext if specified, sets the security context of the container
	// +optional
	SecurityContext *corev1.SecurityContext `json:"securityContext,omitempty"`

	// LivenessProbe for the container
	// +optional
	LivenessProbe *corev1.Probe `json:"livenessProbe,omitempty"`

	// ReadinessProbe for the container
	// +optional
	ReadinessProbe *corev1.Probe `json:"readinessProbe,omitempty"`
}

// +kubebuilder:object:root=true
// HostedControlPlaneList contains a list of HostedControlPlanes.
type WorkloadConfigurationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []WorkloadConfiguration `json:"items"`
}
