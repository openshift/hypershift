package v1beta1

import (
	operatorv1 "github.com/openshift/api/operator/v1"
)

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

	// ovnKubernetesConfig holds OVN-Kubernetes specific configuration.
	// This is only consumed when NetworkType is OVNKubernetes.
	// +optional
	OVNKubernetesConfig *OVNKubernetesConfig `json:"ovnKubernetesConfig,omitempty"`
}

// OVNKubernetesConfig contains OVN-Kubernetes specific configuration options.
// https://github.com/openshift/api/blob/6d3c4e25a8d3aeb57ad61649d80c38cbd27d1cc8/operator/v1/types_network.go#L400-L471
// +kubebuilder:validation:XValidation:rule="!has(self.ipv4) || !has(self.ipv4.internalJoinSubnet) || !has(self.ipv4.internalTransitSwitchSubnet) || self.ipv4.internalJoinSubnet != self.ipv4.internalTransitSwitchSubnet", message="internalJoinSubnet and internalTransitSwitchSubnet must not be the same"
// +kubebuilder:validation:MinProperties=1
type OVNKubernetesConfig struct {
	// v4InternalSubnet is a v4 subnet used internally by ovn-kubernetes in case the
	// default one is being already used by something else. It must not overlap with
	// any other subnet being used by OpenShift or by the node network. The size of the
	// subnet must be larger than the number of nodes. The value cannot be changed after
	// cluster creation.
	// Default is 100.64.0.0/16
	// The value must be in proper IPV4 CIDR format
	// This field is immutable.
	// +kubebuilder:validation:MaxLength=18
	// +kubebuilder:validation:MinLength=9
	// +kubebuilder:validation:XValidation:rule="self.matches('^([0-9]{1,3}\\\\.){3}[0-9]{1,3}/([0-9]|[1-2][0-9]|3[0-2])$') && self.split('/')[0].split('.').all(oct, int(oct) >= 0 && int(oct) <= 255)", message="Subnet must be in a valid IPv4 CIDR format"
	// +kubebuilder:validation:XValidation:rule="self.matches('^.*/[0-9]+$') && int(self.split('/')[1]) <= 30", message="subnet must be in the range /0 to /30 inclusive"
	// +kubebuilder:validation:XValidation:rule="self.matches('^[0-9]{1,3}\\\\..*') && int(self.split('/')[0].split('.')[0]) > 0", message="first IP address octet must not be 0"
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="v4InternalSubnet is immutable"
	// +optional
	// +immutable
	V4InternalSubnet string `json:"v4InternalSubnet,omitempty"`

	// v6InternalSubnet is a v6 subnet used internally by ovn-kubernetes in case the
	// default one is being already used by something else. It must not overlap with
	// any other subnet being used by OpenShift or by the node network. The size of the
	// subnet must be larger than the number of nodes. The value cannot be changed after
	// cluster creation.
	// Default is fd98::/64
	// The value must be in proper IPV6 CIDR format
	// This field is immutable.
	// +kubebuilder:validation:MaxLength=43
	// +kubebuilder:validation:MinLength=3
	// +kubebuilder:validation:XValidation:rule="self == oldSelf", message="v6InternalSubnet is immutable"
	// +optional
	// +immutable
	V6InternalSubnet string `json:"v6InternalSubnet,omitempty"`

	// ipv4 allows users to configure IP settings for IPv4 connections. When omitted,
	// this means no opinions and the default configuration is used. Check individual
	// fields within ipv4 for details of default values.
	// +optional
	IPv4 *OVNIPv4Config `json:"ipv4,omitempty"`
}

// OVNIPv4Config contains IPv4-specific configuration options for OVN-Kubernetes.
// https://github.com/openshift/api/blob/6d3c4e25a8d3aeb57ad61649d80c38cbd27d1cc8/operator/v1/types_network.go#L473-L503
// +kubebuilder:validation:MinProperties=1
type OVNIPv4Config struct {
	// internalTransitSwitchSubnet is a v4 subnet in IPV4 CIDR format used internally
	// by OVN-Kubernetes for the distributed transit switch in the OVN Interconnect
	// architecture that connects the cluster routers on each node together to enable
	// east west traffic. The subnet chosen should not overlap with other networks
	// specified for OVN-Kubernetes as well as other networks used on the host.
	// When omitted, this means no opinion and the platform is left to choose a reasonable
	// default which is subject to change over time.
	// The current default subnet is 100.88.0.0/16
	// The subnet must be large enough to accommodate one IP per node in your cluster
	// The value must be in proper IPV4 CIDR format
	// +kubebuilder:validation:MaxLength=18
	// +kubebuilder:validation:MinLength=9
	// +kubebuilder:validation:XValidation:rule="self.matches('^([0-9]{1,3}\\\\.){3}[0-9]{1,3}/([0-9]|[1-2][0-9]|3[0-2])$') && self.split('/')[0].split('.').all(oct, int(oct) >= 0 && int(oct) <= 255)", message="Subnet must be in a valid IPv4 CIDR format"
	// +kubebuilder:validation:XValidation:rule="self.matches('^.*/[0-9]+$') && int(self.split('/')[1]) <= 30", message="subnet must be in the range /0 to /30 inclusive"
	// +kubebuilder:validation:XValidation:rule="self.matches('^[0-9]{1,3}\\\\..*') && int(self.split('/')[0].split('.')[0]) > 0", message="first IP address octet must not be 0"
	// +optional
	InternalTransitSwitchSubnet string `json:"internalTransitSwitchSubnet,omitempty"`
	// internalJoinSubnet is a v4 subnet used internally by ovn-kubernetes in case the
	// default one is being already used by something else. It must not overlap with
	// any other subnet being used by OpenShift or by the node network. The size of the
	// subnet must be larger than the number of nodes.
	// The current default value is 100.64.0.0/16
	// The subnet must be large enough to accommodate one IP per node in your cluster
	// The value must be in proper IPV4 CIDR format
	// +kubebuilder:validation:MaxLength=18
	// +kubebuilder:validation:MinLength=9
	// +kubebuilder:validation:XValidation:rule="self.matches('^([0-9]{1,3}\\\\.){3}[0-9]{1,3}/([0-9]|[1-2][0-9]|3[0-2])$') && self.split('/')[0].split('.').all(oct, int(oct) >= 0 && int(oct) <= 255)", message="Subnet must be in a valid IPv4 CIDR format (e.g., 192.168.1.1/24)"
	// +kubebuilder:validation:XValidation:rule="self.matches('^.*/[0-9]+$') && int(self.split('/')[1]) <= 30", message="subnet must be in the range /0 to /30 inclusive"
	// +kubebuilder:validation:XValidation:rule="self.matches('^[0-9]{1,3}\\\\..*') && int(self.split('/')[0].split('.')[0]) > 0", message="first IP address octet must not be 0"
	// +optional
	InternalJoinSubnet string `json:"internalJoinSubnet,omitempty"`
}

// IngressOperatorSpec is the specification of the desired behavior of the Ingress Operator.
type IngressOperatorSpec struct {
	// endpointPublishingStrategy is used to publish the default ingress controller endpoints.
	//
	// The endpoint publishing strategy is determined by the following precedence order:
	// 1. User-specified endpointPublishingStrategy (highest priority) - if this field is set,
	//    it takes precedence over all other configuration methods
	// 2. Platform-specific defaults with annotation overrides - if no user strategy is set,
	//    the platform type determines the default strategy, which can be further modified by:
	//    - hypershift.openshift.io/private-ingress-controller annotation (sets PrivateStrategyType)
	//    - hypershift.openshift.io/ingress-controller-load-balancer-scope annotation (sets LoadBalancerScope)
	// 3. Generic LoadBalancer fallback - if the platform is not recognized, defaults to
	//    LoadBalancerService with External scope
	//
	// Platform-specific defaults when endpointPublishingStrategy is not set:
	// - AWS: LoadBalancerService with External scope (or NLB if configured)
	// - Azure, GCP: LoadBalancerService with External scope
	// - IBMCloud: LoadBalancerService with External scope (or NodePort for UPI)
	// - None: HostNetwork
	// - KubeVirt: NodePortService
	// - OpenStack: LoadBalancerService with External scope and optional FloatingIP
	// - Other platforms: LoadBalancerService with External scope
	//
	// See the OpenShift Ingress Operator EndpointPublishingStrategy type for the full specification:
	// https://github.com/openshift/api/blob/master/operator/v1/types_ingress.go
	// +optional
	// +kubebuilder:pruning:PreserveUnknownFields
	// +kubebuilder:validation:Type=object
	EndpointPublishingStrategy *operatorv1.EndpointPublishingStrategy `json:"endpointPublishingStrategy,omitempty"`
}
