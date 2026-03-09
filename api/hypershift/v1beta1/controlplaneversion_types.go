package v1beta1

import (
	configv1 "github.com/openshift/api/config/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ControlPlaneVersionStatus tracks the rollout state of management-side control plane components.
// It records the desired release, a pruned history of version transitions (newest first), and
// the last observed generation of the HostedControlPlane spec.
// +k8s:deepcopy-gen=true
type ControlPlaneVersionStatus struct {
	// desired is the release version that the control plane is reconciling towards.
	// It is derived from the HostedControlPlane release image fields.
	// +required
	Desired configv1.Release `json:"desired,omitempty,omitzero"`

	// history contains a list of versions applied to management-side control plane components. The newest entry is
	// first in the list. Entries have state Completed when all ControlPlaneComponent resources report the target
	// version with RolloutComplete=True. Entries have state Partial when the rollout is in progress or has failed.
	// +optional
	// +listType=atomic
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=100
	History []ControlPlaneUpdateHistory `json:"history,omitempty"`

	// observedGeneration reports which generation of the HostedControlPlane spec is being synced.
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=9007199254740992
	ObservedGeneration int64 `json:"observedGeneration,omitempty,omitzero"`
}

// ControlPlaneUpdateHistory is a record of a single version transition for management-side
// control plane components. Each entry captures the target version, its release image, when
// the rollout started, and when (or whether) it completed.
// +k8s:deepcopy-gen=true
type ControlPlaneUpdateHistory struct {
	// state reflects whether the update was fully applied. The Partial state
	// indicates the update is not fully applied, while the Completed state
	// indicates the update was successfully rolled out.
	// +required
	// +kubebuilder:validation:Enum=Completed;Partial
	State configv1.UpdateState `json:"state,omitempty"`

	// startedTime is the time at which the update was started.
	// +required
	StartedTime metav1.Time `json:"startedTime,omitempty,omitzero"`

	// completionTime is the time at which the update completed. It is set
	// when all management-side components have reached the target version.
	// It is not set while the update is in progress.
	// +optional
	CompletionTime metav1.Time `json:"completionTime,omitempty,omitzero"`

	// version is a semantic version string identifying the update version
	// (e.g. "4.20.1").
	// +required
	// +kubebuilder:validation:XValidation:rule=`self.matches('^\\d+\\.\\d+\\.\\d+.*')`,message="version must start with semantic version prefix x.y.z"
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	Version string `json:"version,omitempty"`

	// image is the release image pullspec used for this update.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=447
	Image string `json:"image,omitempty"`
}
