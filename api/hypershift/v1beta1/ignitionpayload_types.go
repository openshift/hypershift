package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func init() {
	SchemeBuilder.Register(func(scheme *runtime.Scheme) error {
		scheme.AddKnownTypes(SchemeGroupVersion,
			&IgnitionPayload{},
			&IgnitionPayloadList{},
		)
		return nil
	})
}

// IgnitionPayload requests an Ignition payload for booting nodes. spec describes the
// inputs used to generate the payload, and status reports the generated payloads and
// rollout progress.
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

	// spec describes the inputs used to generate the payload.
	// +required
	Spec IgnitionPayloadSpec `json:"spec,omitzero"`

	// status reports the generated payloads and rollout progress.
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
	// IgnitionPayloadIgnitionReachedCondition is False after each new rollout until a
	// node has fetched the current payload, then True: the PayloadController resets it to
	// False when rolloutGeneration advances and the serving tier sets it True on fetch.
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

	// pullSecret references the pull-secret Secret in the IgnitionPayload's namespace
	// (conventionally named "pull-secret") that the generator reads to fetch the release
	// image and embed in the generated payload. This field is required and immutable:
	// change the pull secret in place (via the global pull secret mechanism) rather than
	// updating this reference, so it never triggers a node rollout.
	// +required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="pullSecret is immutable; update the pull secret in place via the global pull secret mechanism"
	PullSecret SecretReference `json:"pullSecret,omitzero"`

	// additionalTrustBundle optionally references a ConfigMap in the IgnitionPayload's namespace
	// holding additional CA certificates for booting nodes to trust. The CA bundle is
	// read from the "ca-bundle.crt" key, PEM-encoded. When omitted, no additional
	// trust bundle is embedded in the payload.
	// +optional
	AdditionalTrustBundle ConfigMapReference `json:"additionalTrustBundle,omitzero"`

	// osImageStream selects the RHEL OS stream the payload targets. Valid values are
	// "rhel-9" (RHEL 9 boot images) and "rhel-10" (RHEL 10 boot images). When omitted,
	// the stream defaults to the one implied by the release version (rhel-9 for OCP < 5.0,
	// rhel-10 for OCP >= 5.0). rhel-9 is the legacy stream and is expected to be retired
	// as supported releases move to RHEL 10. If the generator does not support the selected
	// stream, it does not render a payload and reports PayloadGenerated=False.
	// +optional
	OSImageStream OSImageStreamReference `json:"osImageStream,omitzero"`

	// rolloutGlobalConfig references an IgnitionPayload-owned ConfigMap in the
	// IgnitionPayload's namespace holding the rollout-relevant subset of the hosted
	// cluster's global configuration, canonicalized and authored by the consumer. The
	// content is read from the "config" key and feeds the rollout hash; a ConfigMap
	// missing that key contributes nothing. When omitted, no global configuration
	// contributes to the payload.
	// +optional
	RolloutGlobalConfig ConfigMapReference `json:"rolloutGlobalConfig,omitzero"`

	// additionalRolloutInputs is an opaque, consumer-authored summary of rollout-relevant inputs
	// that are not otherwise captured by the referenced ConfigMaps. It is folded into the rollout
	// hash only (never into configHash), so setting or changing it advances rolloutGeneration and
	// rolls running nodes, while the payload bytes (and configHash) are unaffected. Use it to roll
	// running nodes for a change the rollout hash would otherwise miss — typically an input that
	// lives in a managementConfigMaps ConfigMap (which on its own only refreshes the payload for new
	// nodes) but must also roll existing nodes, e.g. the API server endpoint identity (access mode,
	// advertise address, port) carried in the apiserver-HAProxy config. It stays out of configHash
	// so configHash remains the payload's byte identity. Must be printable ASCII with no whitespace
	// so the rollout hash stays deterministic. When omitted, no out-of-band input contributes to the
	// rollout hash.
	// +optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=1024
	// +kubebuilder:validation:XValidation:rule="self.matches('^[!-~]*$')",message="additionalRolloutInputs must contain only printable ASCII characters and no whitespace, so the rollout hash stays deterministic"
	AdditionalRolloutInputs string `json:"additionalRolloutInputs,omitempty"`

	// rolloutConfigMaps lists ConfigMaps in the IgnitionPayload's namespace whose
	// contents are rollout-relevant (user, core, and NTO machine configs). Each referenced
	// ConfigMap provides its content under the "config" key as multi-document YAML of
	// MachineConfig manifests; a ConfigMap missing that key contributes nothing. Names
	// must be unique (enforced as a map keyed by name) and must not also appear in
	// managementConfigMaps. A change to any listed ConfigMap feeds the rollout hash and
	// can therefore trigger a node rollout. When omitted, no rollout-relevant ConfigMaps
	// contribute to the payload. At least 1 and at most 100 entries.
	// +optional
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=100
	RolloutConfigMaps []ConfigMapReference `json:"rolloutConfigMaps,omitempty"`

	// managementConfigMaps lists ConfigMaps in the IgnitionPayload's namespace whose
	// contents are management-side only (e.g. the apiserver-HAProxy config). Each
	// referenced ConfigMap provides its content under the "config" key as multi-document
	// YAML of MachineConfig manifests; a ConfigMap missing that key contributes nothing.
	// Names must be unique (enforced as a map keyed by name) and must not also appear in
	// rolloutConfigMaps. A change to any listed ConfigMap refreshes the generated payload
	// bytes WITHOUT advancing rolloutGeneration, so it does not trigger a node rollout.
	// When omitted, no management-only ConfigMaps contribute to the payload. At least 1
	// and at most 100 entries.
	// +optional
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=100
	ManagementConfigMaps []ConfigMapReference `json:"managementConfigMaps,omitempty"`

	// retiredRolloutGeneration releases the previous payload early. At most two payloads
	// are retained — current and previous — and a new rollout always evicts the old
	// previous regardless of this field. Set it to the highest rolloutGeneration whose
	// nodes have all been replaced to free previous before the next rollout; the current
	// payload is always kept. It only ever increases. Minimum 1.
	// +optional
	// +kubebuilder:validation:Minimum=1
	RetiredRolloutGeneration int64 `json:"retiredRolloutGeneration,omitempty"`
}

// ConfigMapReference references a ConfigMap by name.
// The ConfigMap must exist in the same namespace as the referencing resource.
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

// IgnitionPayloadStatus has two writers. The PayloadController owns current,
// previous, and the PayloadGenerated condition, and resets IgnitionReached to False
// when rolloutGeneration advances; the serving tier sets IgnitionReached to True
// (field-scoped patch) once a node fetches the current payload.
//
// +kubebuilder:validation:MinProperties=1
// +kubebuilder:validation:XValidation:rule="!has(self.previous) || has(self.current)",message="previous requires current"
// +kubebuilder:validation:XValidation:rule="!has(self.previous) || !has(self.current) || self.previous.rolloutGeneration < self.current.rolloutGeneration",message="previous.rolloutGeneration must be lower than current.rolloutGeneration"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.current) || (has(self.current) && self.current.rolloutGeneration >= oldSelf.current.rolloutGeneration)",message="current.rolloutGeneration cannot decrease and current cannot be removed"
type IgnitionPayloadStatus struct {
	// conditions reports generation and rollout progress. Known condition types are
	// PayloadGenerated and IgnitionReached. PayloadGenerated is True when the payload for
	// the current spec has been generated; its observedGeneration is the metadata.generation
	// last processed, so it shows whether current reflects the latest spec. IgnitionReached
	// is False after each new rollout until a node has fetched the current payload, then
	// True. At least 1 and at most 100 entries.
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
	// in-flight boots on the previous generation are served until they drain. It is absent when
	// there is no prior payload (e.g. the first generation) or once the prior payload
	// has fully drained.
	// +optional
	Previous PayloadReference `json:"previous,omitzero"`

	// rolloutHashVersion identifies the version of the formula used to compute
	// current.rolloutHash. It is absent until the first payload has been generated. When a
	// newer formula is adopted, current.rolloutHash is recomputed and this field is updated
	// without advancing rolloutGeneration, so a formula change does not trigger a node
	// rollout. When present, it must be at least 1.
	// +optional
	// +kubebuilder:validation:Minimum=1
	RolloutHashVersion int64 `json:"rolloutHashVersion,omitempty"`
}

// PayloadReference identifies one generated payload version and its store key.
type PayloadReference struct {
	// configHash is the payload-identity hash over the whole validated config. It is an
	// opaque hash string: consumers compare it for equality and must not parse it.
	// It is required and consists of 1 to 64 lowercase hexadecimal characters (0-9, a-f).
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:XValidation:rule="self.matches('^[0-9a-f]+$')",message="configHash must consist of lowercase hexadecimal characters"
	ConfigHash string `json:"configHash,omitempty"`

	// rolloutHash is the hash over the rollout-relevant inputs. It is an opaque hash
	// string: consumers compare it for equality and must not parse it.
	// It is required and consists of 1 to 64 lowercase hexadecimal characters (0-9, a-f).
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:XValidation:rule="self.matches('^[0-9a-f]+$')",message="rolloutHash must consist of lowercase hexadecimal characters"
	RolloutHash string `json:"rolloutHash,omitempty"`

	// userDataSecretName is the name of the generator-authored pointer-ignition Secret in the
	// IgnitionPayload's namespace, formatted `<ignitionpayload-name>-<rolloutGeneration>`. The
	// consumer points its machines' bootstrap dataSecretName at it; it is a non-secret reference.
	// That Secret embeds the per-payload endpoint (`/ignition/<name>`), the CA, a static
	// rollout-generation header, and the bearer token; the token authenticates the fetch, lives
	// only inside that Secret (never in the PayloadStore, which holds only payload bytes keyed by
	// rollout identity, and never in status). Required, at most 253 characters.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	UserDataSecretName string `json:"userDataSecretName,omitempty"`

	// rolloutGeneration is a monotonically increasing counter the consumer watches to execute a
	// rollout. It advances only when rolloutHash changes (so a management-only payload refresh does
	// not advance it); each advance mints a new token, while a refresh re-issues the token with a
	// fresh expiry under the same rollout identity. It is also the serving/storage identity: the
	// PayloadStore entry, the userDataSecretName, and the node's rollout-generation header are all
	// keyed on it, so a rolloutHash formula rebaseline — which rewrites rolloutHash without advancing
	// this counter — never moves serving identity. Minimum 1.
	// +required
	// +kubebuilder:validation:Minimum=1
	RolloutGeneration int64 `json:"rolloutGeneration,omitempty"`
}
