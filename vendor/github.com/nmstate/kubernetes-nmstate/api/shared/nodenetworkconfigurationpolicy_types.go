/*
Copyright The Kubernetes NMState Authors.


Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package shared

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// NodeNetworkConfigurationPolicySpec defines the desired state of NodeNetworkConfigurationPolicy
type NodeNetworkConfigurationPolicySpec struct {
	// NodeSelector is a selector which must be true for the policy to be applied to the node.
	// Selector which must match a node's labels for the policy to be scheduled on that node.
	// More info: https://kubernetes.io/docs/concepts/configuration/assign-pod-node/
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`

	// Capture contains expressions with an associated name than can be referenced
	// at the DesiredState.
	// +optional
	Capture map[string]string `json:"capture,omitempty"`

	// +kubebuilder:validation:XPreserveUnknownFields
	// The desired configuration of the policy
	DesiredState State `json:"desiredState,omitempty"`

	// MaxUnavailable specifies percentage or number
	// of machines that can be updating at a time. Default is "50%".
	// +optional
	MaxUnavailable *intstr.IntOrString `json:"maxUnavailable,omitempty"`
}

// NodeNetworkConfigurationPolicyStatus defines the observed state of NodeNetworkConfigurationPolicy
type NodeNetworkConfigurationPolicyStatus struct {
	Conditions ConditionList `json:"conditions,omitempty" optional:"true"`

	// UnavailableNodeCount represents the total number of potentially unavailable nodes that are
	// processing a NodeNetworkConfigurationPolicy
	// +optional
	UnavailableNodeCount int `json:"unavailableNodeCount,omitempty" optional:"true"`
	// LastUnavailableNodeCountUpdate is time of the last UnavailableNodeCount update
	// +optional
	LastUnavailableNodeCountUpdate *metav1.Time `json:"lastUnavailableNodeCountUpdate,omitempty" optional:"true"`
}

const (
	NodeNetworkConfigurationPolicyConditionAvailable   ConditionType = "Available"
	NodeNetworkConfigurationPolicyConditionDegraded    ConditionType = "Degraded"
	NodeNetworkConfigurationPolicyConditionProgressing ConditionType = "Progressing"
)

var NodeNetworkConfigurationPolicyConditionTypes = [...]ConditionType{
	NodeNetworkConfigurationPolicyConditionAvailable,
	NodeNetworkConfigurationPolicyConditionDegraded,
	NodeNetworkConfigurationPolicyConditionProgressing,
}

const (
	NodeNetworkConfigurationPolicyConditionFailedToConfigure           ConditionReason = "FailedToConfigure"
	NodeNetworkConfigurationPolicyConditionSuccessfullyConfigured      ConditionReason = "SuccessfullyConfigured"
	NodeNetworkConfigurationPolicyConditionConfigurationProgressing    ConditionReason = "ConfigurationProgressing"
	NodeNetworkConfigurationPolicyConditionConfigurationNoMatchingNode ConditionReason = "NoMatchingNode"
)
