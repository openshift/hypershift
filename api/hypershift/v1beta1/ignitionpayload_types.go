package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// IgnitionPayload is one consumer's ignition payload request: the consumer's
// inputs in spec, the generator's results in status. One resource exists per
// consumer request (one per NodePool for the NodePool controller; Karpenter
// creates its own on demand). The type is consumer-agnostic — it carries no
// back-reference to a NodePool so non-NodePool consumers can use it.
//
// +genclient
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:path=ignitionpayloads,shortName=ignpayload,scope=Namespaced
// +kubebuilder:printcolumn:name="Generation",type=integer,JSONPath=".status.current.generation"
// +kubebuilder:printcolumn:name="Generated",type=string,JSONPath=".status.conditions[?(@.type==\"PayloadGenerated\")].status"
// +kubebuilder:printcolumn:name="Reached",type=string,JSONPath=".status.conditions[?(@.type==\"IgnitionReached\")].status"
// +openshift:enable:FeatureGate=IgnitionPayloadSystem
type IgnitionPayload struct {
	metav1.TypeMeta `json:",inline"`
	// metadata is the standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// spec is written by the consumer and describes the desired payload inputs.
	// +required
	Spec IgnitionPayloadSpec `json:"spec,omitzero"`

	// status is written by the PayloadController and reports generation and
	// rollout progress.
	// +optional
	Status IgnitionPayloadStatus `json:"status,omitzero"`
}

// IgnitionPayloadList contains a list of IgnitionPayload.
//
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type IgnitionPayloadList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []IgnitionPayload `json:"items"`
}

// IgnitionPayloadSpec is written entirely by the consumer; the PayloadController
// treats it as read-only input.
type IgnitionPayloadSpec struct {
	// releaseImage is the pullspec of the OCP release whose
	// machine-config-server binaries render the payload.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=447
	ReleaseImage string `json:"releaseImage,omitempty"`

	// pullSecretName is the name of a Secret in the CR's namespace holding the
	// registry pull secret used to fetch the release image and embedded in the
	// payload.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	PullSecretName string `json:"pullSecretName,omitempty"`

	// additionalTrustBundle optionally references a ConfigMap in the CR's
	// namespace holding a PEM CA bundle for booting nodes to trust.
	// +optional
	AdditionalTrustBundle ConfigMapReference `json:"additionalTrustBundle,omitzero"`

	// osStream selects the RHEL OS stream the payload targets.
	// +optional
	// +kubebuilder:validation:Enum=rhel-9;rhel-10
	OSStream string `json:"osStream,omitempty"`

	// rolloutGlobalConfig references a CR-owned ConfigMap in the CR's namespace
	// holding the rollout-relevant subset of the hosted cluster's global
	// configuration, canonicalized and authored by the consumer.
	// +optional
	RolloutGlobalConfig ConfigMapReference `json:"rolloutGlobalConfig,omitzero"`

	// rolloutConfigMaps lists ConfigMaps in the CR's namespace whose contents
	// are rollout-relevant (user, core, and NTO machine configs).
	// +optional
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=100
	RolloutConfigMaps []ConfigMapReference `json:"rolloutConfigMaps,omitempty"`

	// mgmtConfigMaps lists ConfigMaps in the CR's namespace whose contents are
	// management-side only (the apiserver-HAProxy config).
	// +optional
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=100
	MgmtConfigMaps []ConfigMapReference `json:"mgmtConfigMaps,omitempty"`

	// retiredGeneration is a level-triggered signal that the payload of the
	// given generation has drained and its store token may be freed.
	// +optional
	// +kubebuilder:validation:Minimum=1
	RetiredGeneration int64 `json:"retiredGeneration,omitempty"`
}

// ConfigMapReference references a ConfigMap by name in the CR's namespace.
type ConfigMapReference struct {
	// name is the name of a ConfigMap in the same namespace as this resource.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name,omitempty"`
}

// IgnitionPayloadStatus has two writers with disjoint field ownership. The
// PayloadController owns current, previous, and PayloadGenerated; the serving
// tier owns only IgnitionReached (field-scoped patch).
//
// +kubebuilder:validation:MinProperties=1
type IgnitionPayloadStatus struct {
	// conditions reports generation and rollout progress. Known types:
	// "PayloadGenerated" and "IgnitionReached".
	// +optional
	// +listType=map
	// +listMapKey=type
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=100
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// current describes the payload for the latest validated, generated config.
	// +optional
	Current PayloadReference `json:"current,omitzero"`

	// previous describes the immediately prior payload, retained during a
	// rollout so in-flight boots on the old token are served until they drain.
	// +optional
	Previous PayloadReference `json:"previous,omitzero"`

	// rolloutHashVersion identifies the formula version used to compute
	// current.rolloutHash.
	// +optional
	// +kubebuilder:validation:Minimum=1
	RolloutHashVersion int64 `json:"rolloutHashVersion,omitempty"`
}

// PayloadReference identifies one generated payload version and its store key.
type PayloadReference struct {
	// configHash is the payload-identity hash over the whole validated config.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	ConfigHash string `json:"configHash,omitempty"`

	// rolloutHash is the hash over the rollout-relevant inputs.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	RolloutHash string `json:"rolloutHash,omitempty"`

	// token is an opaque, non-derivable UUID: the key into the PayloadStore.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Token string `json:"token,omitempty"`

	// generation is a monotonically increasing counter the consumer watches to
	// execute a rollout. It advances only when rolloutHash changes.
	// +required
	// +kubebuilder:validation:Minimum=1
	Generation int64 `json:"generation,omitempty"`
}
