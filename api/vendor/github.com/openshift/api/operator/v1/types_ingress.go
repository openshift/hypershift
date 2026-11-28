package v1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// Ingress contains configuration options specific to the Ingress Operator itself,
// including how it manages Gateway API integration.
//
// Compatibility level 1: Stable within a major release for a minimum of 12 months or 3 minor releases (whichever is longer).
// +openshift:compatibility-gen:level=1
// +openshift:file-pattern=cvoRunLevel=0000_50,operatorName=ingress,operatorOrdering=02
// +kubebuilder:object:root=true
// +kubebuilder:resource:path=ingresses,scope=Cluster
// +kubebuilder:subresource:status
// +openshift:api-approved.openshift.io=https://github.com/openshift/api/pull/2890
// +openshift:capability=Ingress
// +openshift:enable:FeatureGate=GatewayAPIManagementMode
// +kubebuilder:validation:XValidation:rule="self.metadata.name == 'cluster'",message="Ingress is a singleton; the .metadata.name field must be 'cluster'"
type Ingress struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is the standard object's metadata.
	// More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#metadata
	// +required
	metav1.ObjectMeta `json:"metadata"`

	// spec holds user settable values for configuration.
	// +required
	Spec IngressSpec `json:"spec,omitzero"`

	// status holds observed values from the cluster.
	// +optional
	Status IngressStatus `json:"status,omitzero"`
}

// IngressSpec is the specification of the desired behavior of the Ingress Operator.
// +kubebuilder:validation:MinProperties=1
type IngressSpec struct {
	// gatewayAPI holds configuration for Gateway API integration, including how the
	// ingress operator manages Gateway API CRDs, the OpenShift Gateway API
	// implementation, and its Gateway API controllers.
	//
	// +optional
	GatewayAPI GatewayAPIIngressConfig `json:"gatewayAPI,omitzero"`

	// canary holds configuration for the ingress canary daemonset (the
	// ingress-canary DaemonSet in the openshift-ingress-canary namespace).
	//
	// canary is optional. When omitted, the ingress canary daemonset keeps its
	// default configuration. When specified, canary must set at least one of
	// its fields; an empty object is rejected.
	//
	// +optional
	// +openshift:enable:FeatureGate=IngressCanaryNodePlacement
	Canary IngressCanary `json:"canary,omitzero"`
}

// IngressStatus defines the observed status of the Ingress Operator.
// +kubebuilder:validation:MinProperties=1
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.observedGeneration) || (has(self.observedGeneration) && self.observedGeneration >= oldSelf.observedGeneration)",message="observedGeneration must remain set and only increase once set"
type IngressStatus struct {
	// conditions is a list of conditions and their status.
	//
	// Gateway API CRD management conditions are reported here with the "GatewayAPI" prefix:
	//
	// * "GatewayAPICRDsManaged" indicates whether the ingress operator is actively
	//   managing Gateway API CRDs.
	// * "GatewayAPICRDsPresent" indicates whether Gateway API CRDs exist on the
	//   cluster.
	// * "GatewayAPICRDsCompliant" indicates whether the installed CRDs match the
	//   version expected by this ingress operator release.
	//
	// +listType=map
	// +listMapKey=type
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=32
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// observedGeneration represents the most recent generation observed by the operator and specifies the version of
	// the spec field currently being synced.
	//
	// When omitted, the operator has not yet observed the resource.
	// +optional
	// +kubebuilder:validation:Minimum=1
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// GatewayAPIIngressConfig holds configuration for Gateway API integration in the
// Cluster Ingress Operator.
// +kubebuilder:validation:MinProperties=1
type GatewayAPIIngressConfig struct {
	// managementMode specifies how the Cluster Ingress Operator manages Gateway API
	// Custom Resource Definitions (CRDs), the OpenShift Gateway API implementation,
	// and its Gateway API controllers.
	//
	// Allowed values are "Managed" and "Unmanaged".
	//
	// When omitted or set to "Managed", the ingress operator installs, owns, and
	// upgrades the Gateway API CRDs, protects them with a Validating Admission
	// Policy, and deploys the OpenShift Gateway API implementation and its Gateway
	// API controllers.
	//
	// When set to "Unmanaged", the ingress operator does not install or manage
	// Gateway API CRDs and does not deploy the OpenShift Gateway API implementation
	// or its Gateway API controllers. The cluster administrator or a third-party
	// product is responsible for providing their own CRDs and Gateway controller.
	// The ingress operator reports observational status only.
	//
	// +optional
	ManagementMode GatewayAPIManagementMode `json:"managementMode,omitempty"`
}

// GatewayAPIManagementMode describes how the Cluster Ingress Operator manages
// Gateway API Custom Resource Definitions.
// +kubebuilder:validation:Enum=Managed;Unmanaged
type GatewayAPIManagementMode string

const (
	// GatewayAPIManagementModeManaged means the ingress operator installs, owns,
	// protects (via a Validating Admission Policy), and upgrades the Gateway API
	// CRDs, deploys the OpenShift Gateway API implementation, and runs its Gateway
	// API controllers. This is the default mode and the only fully supported
	// configuration.
	GatewayAPIManagementModeManaged GatewayAPIManagementMode = "Managed"

	// GatewayAPIManagementModeUnmanaged means the ingress operator does not
	// install or manage Gateway API CRDs, does not deploy the OpenShift Gateway
	// API implementation, and does not run its Gateway API controllers. The
	// cluster administrator or a third-party product is responsible for bringing
	// their own CRDs and Gateway controller. The ingress operator reports
	// observational status only.
	GatewayAPIManagementModeUnmanaged GatewayAPIManagementMode = "Unmanaged"
)

// IngressCanary holds configuration for the ingress canary daemonset.
// +kubebuilder:validation:MinProperties=1
type IngressCanary struct {
	// nodePlacement enables explicit control over the scheduling of the ingress
	// canary daemonset pods.
	//
	// nodePlacement is optional. To leave the canary daemonset with its default
	// scheduling configuration, which tolerates the
	// node-role.kubernetes.io/infra taint, omit the parent canary field
	// entirely: because canary requires at least one field, nodePlacement can
	// only be absent when canary itself is absent. When specified,
	// nodePlacement must set at least one of its fields; an empty object is
	// rejected.
	//
	// +optional
	NodePlacement CanaryNodePlacement `json:"nodePlacement,omitzero"`
}

// CanaryNodePlacement describes node placement configuration for the ingress
// canary daemonset pods.
// +kubebuilder:validation:MinProperties=1
type CanaryNodePlacement struct {
	// tolerations is a list of tolerations that are applied to the ingress
	// canary daemonset pods in addition to the tolerations the canary daemonset
	// has by default. The default toleration for the
	// node-role.kubernetes.io/infra taint is always preserved.
	//
	// Each toleration is plumbed directly to the Pod specification of the
	// canary daemonset pods: it is applied unmodified to the pods' tolerations
	// and has the same semantics as the corresponding fields of the Kubernetes
	// core/v1 Toleration type.
	//
	// If unset, the canary daemonset has only its default tolerations. When
	// specified, the list must contain at least 1 toleration; to remove all
	// additional tolerations, omit the entire spec.canary field rather than
	// setting an empty list or leaving empty parent objects. Retain at least one
	// other valid spec field, such as spec.gatewayAPI, because spec must not be empty.
	//
	// Each toleration has the following constraints:
	//
	// * operator is required and must be "Exists" or "Equal"; omitted and empty
	//   operators, as well as "Lt" and "Gt", are rejected.
	// * effect is required and must be "NoSchedule", "PreferNoSchedule", or
	//   "NoExecute"; omitted and empty effects are rejected.
	// * value must be empty when operator is "Exists".
	// * key must be non-empty. A toleration with an empty key is rejected; in
	//   particular, a toleration with an empty key and operator "Exists", which
	//   tolerates all taints, is rejected.
	// * key must be a qualified label key: a 1-63 character name consisting of
	//   alphanumeric characters, '-', '_', or '.', beginning and ending with an
	//   alphanumeric character, optionally preceded by a lowercase DNS subdomain
	//   prefix of at most 253 characters and a '/'. The total limit is 317 characters.
	// * value for "Equal" must be empty or a label value of at most 63 characters,
	//   using the same characters and beginning/end constraints as the key name.
	// * tolerationSeconds may be specified only when effect is "NoExecute".
	//
	// At least 1 and no more than 32 tolerations may be specified.
	//
	// List order is preserved, but duplicate tolerations are rejected: two
	// tolerations must not have the same key, operator, value, and effect,
	// regardless of their tolerationSeconds. Two tolerations are duplicates
	// only when they also agree on whether value is set; if both set value,
	// the values must be equal. Overlapping tolerations (for example, the same
	// key with a different effect) are allowed.
	// For NoExecute, omitted tolerationSeconds means tolerating indefinitely;
	// finite values are preserved, including zero and negative values.
	//
	// See https://kubernetes.io/docs/concepts/configuration/taint-and-toleration/
	//
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=32
	// +kubebuilder:validation:XValidation:rule=`self.all(x, self.filter(y, y.key == x.key && y.operator == x.operator && y.effect == x.effect && has(y.value) == has(x.value) && (!has(x.value) || y.value == x.value)).size() <= 1)`,message="tolerations must not contain duplicates with the same key, operator, value, and effect"
	Tolerations []CanaryToleration `json:"tolerations,omitempty"`
}

// CanaryToleration describes a toleration added to the ingress canary daemonset.
//
// A CanaryToleration is plumbed directly to the Pod specification of the
// canary daemonset pods and corresponds to a Kubernetes core/v1 Toleration.
// Its fields have the same semantics as their core/v1 Toleration counterparts,
// with the validation constraints documented on each field.
//
// +kubebuilder:validation:XValidation:rule=`!has(self.operator) || self.operator != 'Exists' || !has(self.value)`,message="tolerations value must not be set when operator is 'Exists'"
// +kubebuilder:validation:XValidation:rule=`!has(self.operator) || self.operator != 'Equal' || !has(self.value) || !format.labelValue().validate(self.value).hasValue()`,message="tolerations Equal value must be a valid label value of at most 63 characters"
// +kubebuilder:validation:XValidation:rule=`!has(self.tolerationSeconds) || (has(self.effect) && self.effect == 'NoExecute')`,message="tolerations effect must be 'NoExecute' when tolerationSeconds is set"
type CanaryToleration struct {
	// key is required and identifies the taint to tolerate. It must be a qualified
	// label key of 1-317 characters: an alphanumeric-bounded name of 1-63
	// characters using alphanumerics, '-', '_', or '.', optionally preceded by
	// a lowercase DNS subdomain prefix of at most 253 characters and a '/'.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=317
	// +kubebuilder:validation:XValidation:rule=`!format.qualifiedName().validate(self).hasValue()`,message="tolerations key must be a qualified label key with a name of 1-63 characters and an optional DNS prefix of at most 253 characters"
	Key string `json:"key,omitempty"`

	// operator is required and specifies how the taint value is matched.
	// Equal matches the specified value; Exists matches any value and requires
	// value to be empty or omitted. Only Equal and Exists are allowed;
	// omitted or empty operators are rejected.
	// +required
	Operator CanaryTolerationOperator `json:"operator,omitempty"`

	// value is optional; when omitted, no value is matched. For Equal, it must be
	// a non-empty label value of at most 63 characters, consisting of
	// alphanumerics, '-', '_', or '.', beginning and ending with an alphanumeric
	// character. The empty string is not allowed: omit value instead, as there is
	// no semantic difference between the two. For Exists, it must be omitted.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Value string `json:"value,omitempty"`

	// effect is required and specifies the taint effect to match. Only NoSchedule,
	// PreferNoSchedule, and NoExecute are allowed; omitted or empty effects are
	// rejected. NoSchedule prevents scheduling, PreferNoSchedule expresses a
	// scheduling preference, and NoExecute also evicts pods that do not tolerate it.
	// +required
	Effect CanaryTolerationEffect `json:"effect,omitempty"`

	// tolerationSeconds is optional and may be set only for effect NoExecute.
	// When omitted, the taint is tolerated indefinitely. When specified, this is
	// the duration in seconds for which the taint is tolerated. Zero and negative
	// values are preserved by the API and treated as zero by Kubernetes.
	// +optional
	TolerationSeconds *int64 `json:"tolerationSeconds,omitempty"`
}

// CanaryTolerationEffect is the effect of a taint that an ingress canary
// toleration matches.
// +kubebuilder:validation:Enum=NoSchedule;PreferNoSchedule;NoExecute
// +enum
type CanaryTolerationEffect string

const (
	// CanaryTolerationEffectNoSchedule prevents scheduling of pods that do not
	// tolerate the taint.
	CanaryTolerationEffectNoSchedule CanaryTolerationEffect = "NoSchedule"

	// CanaryTolerationEffectPreferNoSchedule expresses a scheduling preference
	// against scheduling pods that do not tolerate the taint.
	CanaryTolerationEffectPreferNoSchedule CanaryTolerationEffect = "PreferNoSchedule"

	// CanaryTolerationEffectNoExecute prevents scheduling and evicts running
	// pods that do not tolerate the taint.
	CanaryTolerationEffectNoExecute CanaryTolerationEffect = "NoExecute"
)

// CanaryTolerationOperator describes how an ingress canary toleration matches
// the value of the taint it tolerates.
// +kubebuilder:validation:Enum=Exists;Equal
// +enum
type CanaryTolerationOperator string

const (
	// CanaryTolerationOperatorExists matches all taints that have the
	// toleration's key, regardless of the taint's value.
	CanaryTolerationOperatorExists CanaryTolerationOperator = "Exists"

	// CanaryTolerationOperatorEqual matches taints whose value equals the
	// toleration's value.
	CanaryTolerationOperatorEqual CanaryTolerationOperator = "Equal"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// IngressList is a collection of Ingresses.
//
// Compatibility level 1: Stable within a major release for a minimum of 12 months or 3 minor releases (whichever is longer).
// +openshift:compatibility-gen:level=1
type IngressList struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is the standard list's metadata.
	// More info: https://git.k8s.io/community/contributors/devel/sig-architecture/api-conventions.md#metadata
	metav1.ListMeta `json:"metadata"`

	// items is a list of Ingresses.
	// +optional
	Items []Ingress `json:"items,omitempty"`
}
