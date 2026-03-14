package util

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"
)

// isControlPlaneVersionCompleted checks if the control plane version has
// reached Completed state with the desired image. Used by WaitForControlPlaneRollout.
func isControlPlaneVersionCompleted(hc *hyperv1.HostedCluster) (done bool, reasons string, err error) {
	if hc.Status.ControlPlaneVersion == nil {
		return false, "HostedCluster has no controlPlaneVersion status", nil
	}
	if len(hc.Status.ControlPlaneVersion.History) == 0 {
		return false, "HostedCluster controlPlaneVersion has no history", nil
	}
	entry := hc.Status.ControlPlaneVersion.History[0]
	if entry.Image != hc.Status.ControlPlaneVersion.Desired.Image {
		return false, fmt.Sprintf("controlPlaneVersion desired image %s doesn't match most recent image in history %s",
			hc.Status.ControlPlaneVersion.Desired.Image, entry.Image), nil
	}
	if entry.State != configv1.CompletedUpdate {
		return false, fmt.Sprintf("controlPlaneVersion state is %s, waiting for Completed", entry.State), nil
	}
	return true, "controlPlaneVersion reached Completed", nil
}

// isControlPlaneVersionSteadyState checks if the control plane version is in a
// valid steady state for ValidateHostedClusterConditions. For clusters with
// worker nodes, controlPlaneVersion must be Completed. For zero-worker-node
// clusters, controlPlaneVersion can be Completed even when version is Partial.
func isControlPlaneVersionSteadyState(hc *hyperv1.HostedCluster, hasWorkerNodes bool) bool {
	if hc.Status.ControlPlaneVersion == nil {
		return false
	}
	if len(hc.Status.ControlPlaneVersion.History) == 0 {
		return false
	}
	return hc.Status.ControlPlaneVersion.History[0].State == configv1.CompletedUpdate
}
