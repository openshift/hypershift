package v1beta1

import operatorv1 "github.com/openshift/api/operator/v1"

// ClusterVersionOperatorSpec is the specification of the desired behavior of the Cluster Version Operator.
type ClusterVersionOperatorSpec struct {
	// operatorLogLevel is an intent based logging for the operator itself. It does not give fine-grained control, but it is a
	// simple way to manage coarse grained logging choices that operators have to interpret for themselves.
	//
	// Valid values are: "Normal", "Debug", "Trace", "TraceAll".
	// Defaults to "Normal".
	// +optional
	// +kubebuilder:default=Normal
	OperatorLogLevel operatorv1.LogLevel `json:"operatorLogLevel,omitempty"`
}
