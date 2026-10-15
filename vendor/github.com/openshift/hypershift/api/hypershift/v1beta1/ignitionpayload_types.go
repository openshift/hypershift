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
// +kubebuilder:printcolumn:name="RolloutGeneration",type=integer,JSONPath=".status.current.rolloutGeneration"
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

// Condition types reported in IgnitionPayloadStatus.Conditions.
const (
	// IgnitionPayloadPayloadGeneratedCondition is set by the PayloadController to
	// report whether the payload for the current spec has been generated. Its
	// observedGeneration tracks the spec generation last processed, so consumers can
	// tell whether status.current reflects the latest spec.
	IgnitionPayloadPayloadGeneratedCondition = "PayloadGenerated"
	// IgnitionPayloadIgnitionReachedCondition is set by the serving tier to report
	// that a booting node has fetched (reached) its ignition payload.
	IgnitionPayloadIgnitionReachedCondition = "IgnitionReached"
)

// IgnitionPayloadSpec is written entirely by the consumer; the PayloadController
// treats it as read-only input.
//
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.retiredRolloutGeneration) || (has(self.retiredRolloutGeneration) && self.retiredRolloutGeneration >= oldSelf.retiredRolloutGeneration)",message="retiredRolloutGeneration cannot decrease or be removed"
// +kubebuilder:validation:XValidation:rule="!has(self.rolloutConfigMaps) || !has(self.managementConfigMaps) || self.rolloutConfigMaps.all(r, !self.managementConfigMaps.exists(m, m.name == r.name))",message="a ConfigMap cannot be listed in both rolloutConfigMaps and managementConfigMaps"
type IgnitionPayloadSpec struct {
	// releaseImage is the pullspec of the OCP release whose machine-config-server
	// binaries render the payload. It is resolved to an immutable digest before the
	// payload is generated. This field is required.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:XValidation:rule=`self.matches('^(\\w+\\S+)$')`,message="releaseImage must start with a word character (letters, digits, or underscores) and contain no whitespace"
	ReleaseImage string `json:"releaseImage,omitempty"`

	// pullSecret references a Secret in the CR's namespace holding the registry pull
	// secret used to fetch the release image and embedded in the generated payload.
	// This field is required.
	// +required
	PullSecret SecretReference `json:"pullSecret,omitzero"`

	// additionalTrustBundle optionally references a ConfigMap in the CR's namespace
	// holding additional CA certificates for booting nodes to trust. The CA bundle is
	// read from the "ca-bundle.crt" key, PEM-encoded. When omitted, no additional
	// trust bundle is embedded in the payload.
	// +optional
	AdditionalTrustBundle ConfigMapReference `json:"additionalTrustBundle,omitzero"`

	// osStream selects the RHEL OS stream the payload targets, by name (one of
	// "rhel-9" or "rhel-10"). When omitted, the stream defaults to the one implied by
	// the release version. Consumers that do not recognize the selected stream (e.g. a
	// future stream) should treat the payload as unservable rather than guessing. This
	// reuses the NodePool OSImageStream model.
	// +optional
	OSStream OSImageStreamReference `json:"osStream,omitzero"`

	// rolloutGlobalConfig references a CR-owned ConfigMap in the CR's namespace holding
	// the rollout-relevant subset of the hosted cluster's global configuration,
	// canonicalized and authored by the consumer. The content is an opaque,
	// canonicalized representation that feeds the rollout hash. When omitted, no global
	// configuration contributes to the payload.
	// +optional
	RolloutGlobalConfig ConfigMapReference `json:"rolloutGlobalConfig,omitzero"`

	// rolloutConfigMaps lists ConfigMaps in the CR's namespace whose contents are
	// rollout-relevant (user, core, and NTO machine configs). Names must be unique
	// (enforced as a map keyed by name) and must not also appear in
	// managementConfigMaps. A change to any listed ConfigMap feeds the rollout hash and
	// can therefore trigger a node rollout. When omitted, no rollout-relevant
	// ConfigMaps contribute to the payload. At least 1 and at most 100 entries.
	// +optional
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=100
	RolloutConfigMaps []ConfigMapReference `json:"rolloutConfigMaps,omitempty"`

	// managementConfigMaps lists ConfigMaps in the CR's namespace whose contents are
	// management-side only (e.g. the apiserver-HAProxy config). Names must be unique
	// (enforced as a map keyed by name) and must not also appear in rolloutConfigMaps.
	// A change to any listed ConfigMap refreshes the generated payload bytes WITHOUT
	// advancing rolloutGeneration, so it does not trigger a node rollout. When omitted,
	// no management-only ConfigMaps contribute to the payload. At least 1 and at most
	// 100 entries.
	// +optional
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=100
	ManagementConfigMaps []ConfigMapReference `json:"managementConfigMaps,omitempty"`

	// retiredRolloutGeneration is a level-triggered signal that the payload at or below
	// this rolloutGeneration has drained and its store token may be freed, except for
	// the current rolloutGeneration which is never retired. It only ever increases. When
	// omitted, nothing is retired. Minimum 1.
	// +optional
	// +kubebuilder:validation:Minimum=1
	RetiredRolloutGeneration int64 `json:"retiredRolloutGeneration,omitempty"`
}

// ConfigMapReference references a ConfigMap by name in the CR's namespace.
type ConfigMapReference struct {
	// name is the name of a ConfigMap in the same namespace as this resource. It must
	// be a valid DNS-1123 subdomain: at most 253 characters, consisting of lowercase
	// alphanumeric characters, hyphens, and periods. Each period-separated segment must
	// start and end with an alphanumeric character.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:XValidation:rule="self.matches('^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\\\\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$')",message="name must consist only of lowercase alphanumeric characters, hyphens, and periods. Each period-separated segment must start and end with an alphanumeric character."
	Name string `json:"name,omitempty"`
}

// IgnitionPayloadStatus has two writers with disjoint field ownership. The
// PayloadController owns current, previous, and PayloadGenerated; the serving
// tier owns only IgnitionReached (field-scoped patch).
//
// +kubebuilder:validation:MinProperties=1
// +kubebuilder:validation:XValidation:rule="!has(self.previous) || has(self.current)",message="previous requires current"
// +kubebuilder:validation:XValidation:rule="!has(self.previous) || !has(self.current) || self.previous.rolloutGeneration < self.current.rolloutGeneration",message="previous.rolloutGeneration must be lower than current.rolloutGeneration"
// +kubebuilder:validation:XValidation:rule="!has(self.previous) || !has(self.current) || self.previous.token != self.current.token",message="previous and current must not share a token"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.current) || (has(self.current) && self.current.rolloutGeneration >= oldSelf.current.rolloutGeneration)",message="current.rolloutGeneration cannot decrease and current cannot be removed"
type IgnitionPayloadStatus struct {
	// conditions reports generation and rollout progress. Known condition types are
	// "PayloadGenerated" (owned by the PayloadController; its observedGeneration tracks
	// the spec generation last processed, so consumers can tell whether current reflects
	// the latest spec) and "IgnitionReached" (owned by the serving tier). At least 1 and
	// at most 100 entries.
	// +optional
	// +listType=map
	// +listMapKey=type
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=100
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// current describes the payload for the latest validated, generated config. It is
	// absent until the first payload has been generated.
	// +optional
	Current PayloadReference `json:"current,omitzero"`

	// previous describes the immediately prior payload, retained during a rollout so
	// in-flight boots on the old token are served until they drain. It is absent when
	// there is no prior payload (e.g. the first generation) or once the prior payload
	// has fully drained.
	// +optional
	Previous PayloadReference `json:"previous,omitzero"`

	// rolloutHashVersion identifies the formula version used to compute
	// current.rolloutHash. Minimum 1.
	// +optional
	// +kubebuilder:validation:Minimum=1
	RolloutHashVersion int64 `json:"rolloutHashVersion,omitempty"`
}

// PayloadReference identifies one generated payload version and its store key.
type PayloadReference struct {
	// configHash is the payload-identity hash over the whole validated config. It is an
	// opaque, fixed-form hash string: consumers compare it for equality and must not
	// parse it. Required, at most 64 characters.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	ConfigHash string `json:"configHash,omitempty"`

	// rolloutHash is the hash over the rollout-relevant inputs. It is an opaque,
	// fixed-form hash string: consumers compare it for equality and must not parse it.
	// Required, at most 64 characters.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	RolloutHash string `json:"rolloutHash,omitempty"`

	// token is an opaque, non-derivable capability: the key into the PayloadStore that
	// the booting node presents to fetch its payload. Because the payload embeds
	// credentials, treat token as a bearer secret. It is a canonical lowercase UUID.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=36
	// +kubebuilder:validation:XValidation:rule="self.matches('^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$')",message="token must be a canonical lowercase UUID"
	Token string `json:"token,omitempty"`

	// rolloutGeneration is a monotonically increasing counter the consumer watches to
	// execute a rollout. It advances only when rolloutHash changes (so a management-only
	// payload refresh does not advance it). Minimum 1.
	// +required
	// +kubebuilder:validation:Minimum=1
	RolloutGeneration int64 `json:"rolloutGeneration,omitempty"`
}
