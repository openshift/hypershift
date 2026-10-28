package v1beta1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// InstallationStage describes the current phase of a HostedCluster or
// HostedControlPlane during initial provisioning. The value progresses
// forward through the defined stages and never moves backward.
//
// +kubebuilder:validation:Enum=Initializing;ControlPlaneProvisioning;WaitingForKubeconfig;WaitingForOperators;InstallationComplete
type InstallationStage string

const (
	// InstallationStageInitializing indicates the cluster has been created
	// but no control plane components have started yet.
	InstallationStageInitializing InstallationStage = "Initializing"

	// InstallationStageControlPlaneProvisioning indicates that control plane
	// components are being created and started.
	InstallationStageControlPlaneProvisioning InstallationStage = "ControlPlaneProvisioning"

	// InstallationStageWaitingForKubeconfig indicates that all control plane
	// components are available and the cluster is waiting for a kubeconfig
	// secret to be created.
	InstallationStageWaitingForKubeconfig InstallationStage = "WaitingForKubeconfig"

	// InstallationStageWaitingForOperators indicates that a kubeconfig is
	// available and the cluster is waiting for the ClusterVersion to report
	// all operators as available.
	InstallationStageWaitingForOperators InstallationStage = "WaitingForOperators"

	// InstallationStageInstallationComplete indicates that the cluster
	// installation has completed successfully.
	InstallationStageInstallationComplete InstallationStage = "InstallationComplete"
)

// InstallationStatus tracks the progress of initial cluster provisioning.
// +kubebuilder:validation:MinProperties=1
type InstallationStatus struct {
	// stage is the current phase of the installation lifecycle.
	// The value progresses forward through the defined stages and never
	// moves backward.
	// +optional
	// +kubebuilder:validation:Enum=Initializing;ControlPlaneProvisioning;WaitingForKubeconfig;WaitingForOperators;InstallationComplete
	Stage InstallationStage `json:"stage,omitempty"`

	// lastTransitionTime is the time at which stage last transitioned
	// to a new value.
	// +optional
	LastTransitionTime metav1.Time `json:"lastTransitionTime,omitempty,omitzero"`
}

// installationStageOrdinal maps each stage to its ordinal position in the
// installation lifecycle. Higher ordinals represent later stages.
var installationStageOrdinal = map[InstallationStage]int{
	InstallationStageInitializing:             0,
	InstallationStageControlPlaneProvisioning: 1,
	InstallationStageWaitingForKubeconfig:     2,
	InstallationStageWaitingForOperators:      3,
	InstallationStageInstallationComplete:     4,
}

// SetInstallationStage enforces forward-only stage transitions on the given
// InstallationStatus. It sets the stage and transition time only when
// newStage is strictly ahead of the current stage. Returns true if the
// stage was updated.
func SetInstallationStage(status *InstallationStatus, newStage InstallationStage, now metav1.Time) bool {
	currentOrd := installationStageOrdinal[status.Stage]
	newOrd, known := installationStageOrdinal[newStage]
	if !known {
		return false
	}
	if status.Stage != "" && newOrd <= currentOrd {
		return false
	}
	status.Stage = newStage
	status.LastTransitionTime = now
	return true
}
