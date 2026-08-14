package v1

import (
	"encoding/json"
	"reflect"
	"strings"

	"k8s.io/apimachinery/pkg/runtime"
)

// kubeletConfigKnownFields is the set of JSON keys corresponding to the explicitly typed
// fields in KubeletConfiguration. It is derived from the struct's json tags at init time
// so it stays in sync automatically when fields are added or removed.
var kubeletConfigKnownFields map[string]struct{}

func init() {
	t := reflect.TypeOf(KubeletConfiguration{})
	kubeletConfigKnownFields = make(map[string]struct{}, t.NumField())
	for i := range t.NumField() {
		f := t.Field(i)
		if tag, ok := f.Tag.Lookup("json"); ok {
			name, _, _ := strings.Cut(tag, ",")
			if name != "" && name != "-" {
				kubeletConfigKnownFields[name] = struct{}{}
			}
		}
	}
}

// EvictionThreshold is a threshold value for a kubelet eviction signal.
// Values are either a percentage (e.g. "10%") or a Kubernetes quantity (e.g. "100Mi").
// +kubebuilder:validation:MaxLength=64
type EvictionThreshold string

// KubeletConfiguration configures kubelet settings for nodes provisioned by this NodeClass.
// These settings are injected into the node's ignition configuration via MachineConfig.
// The fields listed below are validated at admission time. Additional kubelet configuration
// fields beyond those listed here are also accepted and will be passed through to the node's
// kubelet configuration without validation. Overflow fields bypass all CRD validation;
// invalid overflow values will cause node bootstrap failures (kubelet crash loop) rather
// than admission errors.
// When graduating new fields from overflow to typed fields, match upstream kubelet's
// field names and types exactly. See api/AGENTS.md "KubeletConfiguration Field Graduation"
// for the full strategy.
// +kubebuilder:pruning:PreserveUnknownFields
// +kubebuilder:validation:XValidation:rule="!has(self.imageGCHighThresholdPercent) || !has(self.imageGCLowThresholdPercent) || self.imageGCHighThresholdPercent > self.imageGCLowThresholdPercent",message="imageGCHighThresholdPercent must be greater than imageGCLowThresholdPercent"
// +kubebuilder:validation:XValidation:rule="!has(self.podsPerCore) || !has(self.maxPods) || self.podsPerCore <= self.maxPods",message="podsPerCore must not exceed maxPods"
// +kubebuilder:validation:XValidation:rule="!has(self.evictionSoft) || (has(self.evictionSoftGracePeriod) && self.evictionSoft.all(e, e in self.evictionSoftGracePeriod))",message="evictionSoft entry does not have a matching evictionSoftGracePeriod"
// +kubebuilder:validation:XValidation:rule="!has(self.evictionSoftGracePeriod) || (has(self.evictionSoft) && self.evictionSoftGracePeriod.all(e, e in self.evictionSoft))",message="evictionSoftGracePeriod entry does not have a matching evictionSoft"
// +kubebuilder:validation:XValidation:rule="!has(self.evictionHard) || !has(self.evictionSoft) || self.evictionHard.all(key, !(key in self.evictionSoft) || ((self.evictionSoft[key].endsWith('%') && self.evictionHard[key].endsWith('%')) ? (self.evictionSoft[key].size() <= 1 || self.evictionHard[key].size() <= 1 || double(self.evictionSoft[key].substring(0, self.evictionSoft[key].size() - 1)) >= double(self.evictionHard[key].substring(0, self.evictionHard[key].size() - 1))) : (!(isQuantity(self.evictionSoft[key]) && isQuantity(self.evictionHard[key])) || quantity(self.evictionSoft[key]).compareTo(quantity(self.evictionHard[key])) >= 0)))",message="evictionSoft threshold must be greater than or equal to evictionHard threshold for the same signal (soft eviction should fire before hard)"
type KubeletConfiguration struct {
	// maxPods is the maximum number of pods that can run on a node.
	// The value must be between 1 and 2500, inclusive.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=2500
	// +optional
	MaxPods int32 `json:"maxPods,omitempty"`
	// podsPerCore is the maximum number of pods per core. The value must be between 1 and 2500,
	// inclusive, and cannot exceed maxPods when both are set.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=2500
	// +optional
	PodsPerCore int32 `json:"podsPerCore,omitempty"`
	// systemReserved is a set of ResourceName=ResourceQuantity pairs that describe
	// resources reserved for non-kubernetes components.
	// Currently only cpu, memory, ephemeral-storage, and pid are supported.
	// +kubebuilder:validation:XValidation:message="valid keys for systemReserved are ['cpu','memory','ephemeral-storage','pid']",rule="self.all(x, x=='cpu' || x=='memory' || x=='ephemeral-storage' || x=='pid')"
	// +kubebuilder:validation:XValidation:message="systemReserved value cannot be a negative resource quantity",rule="self.all(x, !self[x].startsWith('-'))"
	// +kubebuilder:validation:XValidation:message="systemReserved values must not be empty",rule="self.all(x, self[x].size() > 0)"
	// +kubebuilder:validation:MinProperties=1
	// +kubebuilder:validation:MaxProperties=20
	// +optional
	SystemReserved map[string]string `json:"systemReserved,omitempty"`
	// kubeReserved is a set of ResourceName=ResourceQuantity pairs that describe
	// resources reserved for kubernetes system components.
	// Currently only cpu, memory, ephemeral-storage, and pid are supported.
	// +kubebuilder:validation:XValidation:message="valid keys for kubeReserved are ['cpu','memory','ephemeral-storage','pid']",rule="self.all(x, x=='cpu' || x=='memory' || x=='ephemeral-storage' || x=='pid')"
	// +kubebuilder:validation:XValidation:message="kubeReserved value cannot be a negative resource quantity",rule="self.all(x, !self[x].startsWith('-'))"
	// +kubebuilder:validation:XValidation:message="kubeReserved values must not be empty",rule="self.all(x, self[x].size() > 0)"
	// +kubebuilder:validation:MinProperties=1
	// +kubebuilder:validation:MaxProperties=20
	// +optional
	KubeReserved map[string]string `json:"kubeReserved,omitempty"`
	// evictionHard is a map of signal names to quantities that defines hard eviction thresholds.
	// +kubebuilder:validation:XValidation:message="valid keys for evictionHard are ['memory.available','nodefs.available','nodefs.inodesFree','imagefs.available','imagefs.inodesFree','pid.available']",rule="self.all(x, x in ['memory.available','nodefs.available','nodefs.inodesFree','imagefs.available','imagefs.inodesFree','pid.available'])"
	// +kubebuilder:validation:XValidation:message="evictionHard values must not be empty",rule="self.all(x, self[x].size() > 0)"
	// +kubebuilder:validation:MinProperties=1
	// +kubebuilder:validation:MaxProperties=20
	// +optional
	EvictionHard map[string]EvictionThreshold `json:"evictionHard,omitempty"`
	// evictionSoft is a map of signal names to quantities that defines soft eviction thresholds.
	// +kubebuilder:validation:XValidation:message="valid keys for evictionSoft are ['memory.available','nodefs.available','nodefs.inodesFree','imagefs.available','imagefs.inodesFree','pid.available']",rule="self.all(x, x in ['memory.available','nodefs.available','nodefs.inodesFree','imagefs.available','imagefs.inodesFree','pid.available'])"
	// +kubebuilder:validation:XValidation:message="evictionSoft values must not be empty",rule="self.all(x, self[x].size() > 0)"
	// +kubebuilder:validation:MinProperties=1
	// +kubebuilder:validation:MaxProperties=20
	// +optional
	EvictionSoft map[string]EvictionThreshold `json:"evictionSoft,omitempty"`
	// evictionSoftGracePeriod is a map of signal names to quantities that defines grace periods
	// for each soft eviction signal.
	// +kubebuilder:validation:XValidation:message="valid keys for evictionSoftGracePeriod are ['memory.available','nodefs.available','nodefs.inodesFree','imagefs.available','imagefs.inodesFree','pid.available']",rule="self.all(x, x in ['memory.available','nodefs.available','nodefs.inodesFree','imagefs.available','imagefs.inodesFree','pid.available'])"
	// +kubebuilder:validation:MinProperties=1
	// +kubebuilder:validation:MaxProperties=20
	// +optional
	EvictionSoftGracePeriod map[string]string `json:"evictionSoftGracePeriod,omitempty"`
	// evictionMaxPodGracePeriod is the maximum allowed grace period (in seconds) to use
	// when terminating pods in response to soft eviction thresholds.
	// +optional
	EvictionMaxPodGracePeriod *int32 `json:"evictionMaxPodGracePeriod,omitempty"`
	// imageGCHighThresholdPercent is the percent of disk usage which triggers image garbage collection.
	// The value must be between 0 and 100, inclusive, and must be greater than imageGCLowThresholdPercent when both are set.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	// +optional
	ImageGCHighThresholdPercent *int32 `json:"imageGCHighThresholdPercent,omitempty"`
	// imageGCLowThresholdPercent is the percent of disk usage to which image garbage collection attempts to free.
	// The value must be between 0 and 100, inclusive, and must be less than imageGCHighThresholdPercent when both are set.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	// +optional
	ImageGCLowThresholdPercent *int32 `json:"imageGCLowThresholdPercent,omitempty"`
	// cpuCFSQuota enables CPU CFS quota enforcement for containers that specify CPU limits.
	// +optional
	CPUCFSQuota *bool `json:"cpuCFSQuota,omitempty"`

	// Overflow holds additional kubelet configuration fields not explicitly defined above.
	// These fields are preserved during serialization and deserialization, allowing arbitrary
	// kubelet configuration to pass through to the node's ignition/MachineConfig.
	Overflow runtime.RawExtension `json:"-"`
}

// UnmarshalJSON implements custom JSON unmarshaling for KubeletConfiguration.
// It deserializes known fields into the struct and captures all additional fields
// into the overflow map for pass-through.
func (k *KubeletConfiguration) UnmarshalJSON(data []byte) error {
	// Zero the receiver so that fields absent from the new input
	// (including Overflow, which is json:"-") do not survive from a
	// previous decode.
	*k = KubeletConfiguration{}

	// Unmarshal known fields via alias to avoid infinite recursion
	type Alias KubeletConfiguration
	aux := &struct{ *Alias }{Alias: (*Alias)(k)}
	if err := json.Unmarshal(data, aux); err != nil {
		return err
	}

	// Unmarshal everything into a raw map
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	// Separate unknown fields into overflow
	for key := range kubeletConfigKnownFields {
		delete(raw, key)
	}
	if len(raw) > 0 {
		overflowBytes, err := json.Marshal(raw)
		if err != nil {
			return err
		}
		k.Overflow = runtime.RawExtension{Raw: overflowBytes}
	}
	return nil
}

// MarshalJSON implements custom JSON marshaling for KubeletConfiguration.
// It serializes the known typed fields and merges any overflow fields back in.
func (k KubeletConfiguration) MarshalJSON() ([]byte, error) {
	// Marshal known fields via alias to avoid infinite recursion
	type Alias KubeletConfiguration
	data, err := json.Marshal((*Alias)(&k))
	if err != nil {
		return nil, err
	}

	if len(k.Overflow.Raw) == 0 {
		return data, nil
	}

	// Merge overflow fields into the output; structured fields win on conflict.
	var overflowMap map[string]json.RawMessage
	if err := json.Unmarshal(k.Overflow.Raw, &overflowMap); err != nil {
		return nil, err
	}
	var structured map[string]json.RawMessage
	if err := json.Unmarshal(data, &structured); err != nil {
		return nil, err
	}
	for key, val := range structured {
		overflowMap[key] = val
	}
	return json.Marshal(overflowMap)
}

// HasTypedFields reports whether any explicitly defined struct fields are set.
// This is used by IsZero, but is separate so we can differentiate the zero case
// from "only overflow fields set". This must be kept in sync with the typed
// fields in KubeletConfiguration.
func (k KubeletConfiguration) HasTypedFields() bool {
	return k.MaxPods != 0 ||
		k.PodsPerCore != 0 ||
		k.SystemReserved != nil ||
		k.KubeReserved != nil ||
		k.EvictionHard != nil ||
		k.EvictionSoft != nil ||
		k.EvictionSoftGracePeriod != nil ||
		k.EvictionMaxPodGracePeriod != nil ||
		k.ImageGCHighThresholdPercent != nil ||
		k.ImageGCLowThresholdPercent != nil ||
		k.CPUCFSQuota != nil
}

// IsZero reports whether the KubeletConfiguration is empty (no typed fields set and
// no overflow fields). This is used by the omitzero JSON tag to determine whether the
// field should be omitted during serialization.
func (k KubeletConfiguration) IsZero() bool {
	return !k.HasTypedFields() && len(k.Overflow.Raw) == 0
}
