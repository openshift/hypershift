package shared

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NodeNetworkStateStatus is the status of the NodeNetworkState of a specific node
type NodeNetworkStateStatus struct {
	// +kubebuilder:validation:XPreserveUnknownFields
	CurrentState                 State       `json:"currentState,omitempty"`
	LastSuccessfulUpdateTime     metav1.Time `json:"lastSuccessfulUpdateTime,omitempty"`
	HostNetworkManagerVersion    string      `json:"hostNetworkManagerVersion,omitempty"`
	HandlerNetworkManagerVersion string      `json:"handlerNetworkManagerVersion,omitempty"`
	HandlerNmstateVersion        string      `json:"handlerNmstateVersion,omitempty"`

	Conditions ConditionList `json:"conditions,omitempty" optional:"true"`
}

const (
	NodeNetworkStateConditionAvailable ConditionType = "Available"
	NodeNetworkStateConditionFailing   ConditionType = "Failing"
)

const (
	NodeNetworkStateConditionFailedToConfigure      ConditionReason = "FailedToConfigure"
	NodeNetworkStateConditionSuccessfullyConfigured ConditionReason = "SuccessfullyConfigured"
)
