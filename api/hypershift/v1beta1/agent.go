package v1beta1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// AgentNodePoolPlatform specifies the configuration of a NodePool when operating
// on the Agent platform.
type AgentNodePoolPlatform struct {
	// AgentLabelSelector contains labels that must be set on an Agent in order to
	// be selected for a Machine.
	// +optional
	AgentLabelSelector *metav1.LabelSelector `json:"agentLabelSelector,omitempty"`
}

// AgentPlatformSpec specifies configuration for agent-based installations.
type AgentPlatformSpec struct {
	// AgentNamespace is the namespace where to search for Agents for this cluster
	AgentNamespace string `json:"agentNamespace"`
}
