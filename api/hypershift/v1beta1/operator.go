package v1beta1

// +kubebuilder:validation:Enum="";Normal;Debug;Trace;TraceAll
type LogLevel string

var (
	// Normal is the default. Normal, working log information, everything is fine, but helpful notices for auditing or
	// common operations. In kube, this is probably glog=2.
	Normal LogLevel = "Normal"

	// Debug is used when something went wrong. Even common operations may be logged, and less helpful but more
	// quantity of notices. In kube, this is probably glog=4.
	Debug LogLevel = "Debug"

	// Trace is used when something went really badly and even more verbose logs are needed. Logging every function
	//call as part of a common operation, to tracing execution of a query. In kube, this is probably glog=6.
	Trace LogLevel = "Trace"

	// TraceAll is used when something is broken at the level of API content/decoding. It will dump complete body
	// content. If you turn this on in a production cluster prepare from serious performance issues and massive amounts
	// of logs. In kube, this is probably glog=8.
	TraceAll LogLevel = "TraceAll"
)

// ClusterVersionOperatorSpec is the specification of the desired behavior of the Cluster Version Operator.
type ClusterVersionOperatorSpec struct {
	// operatorLogLevel is an intent based logging for the operator itself. It does not give fine-grained control,
	// but it is a simple way to manage coarse grained logging choices that operators have to interpret for themselves.
	//
	// Valid values are: "Normal", "Debug", "Trace", "TraceAll".
	// Defaults to "Normal".
	// +optional
	// +kubebuilder:default=Normal
	OperatorLogLevel LogLevel `json:"operatorLogLevel,omitempty"`
}

type ClusterNetworkOperatorSpec struct {
	// disableMultiNetwork when set to true disables the Multus CNI plugin and related components
	// in the hosted cluster. This prevents the installation of multus daemon sets in the
	// guest cluster and the multus-admission-controller in the management cluster.
	// Default is false (Multus is enabled).
	// This field is immutable.
	// This field can only be set to true when NetworkType is "Other". Setting it to true
	// with any other NetworkType will result in a validation error during cluster creation.
	//
	// +optional
	// +kubebuilder:default:=false
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="disableMultiNetwork is immutable"
	// +immutable
	DisableMultiNetwork *bool `json:"disableMultiNetwork,omitempty"` // nolint:kubeapilinter
}
