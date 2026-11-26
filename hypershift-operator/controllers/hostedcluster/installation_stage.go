package hostedcluster

import (
	"context"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// reconcileInstallationStage computes the HostedCluster's installation stage
// from the current state of the HCP, NodePools, and CPCs. It evaluates all
// triggers and relies on SetInstallationStage's forward-only guard to pick
// the highest applicable stage.
func reconcileInstallationStage(ctx context.Context, c client.Client,
	hcluster *hyperv1.HostedCluster, hcp *hyperv1.HostedControlPlane,
	controlPlaneNamespace string) {

	now := metav1.Now()
	install := &hcluster.Status.Installation

	// Terminal: ClusterVersionAvailable=True → InstallationComplete (early return).
	if hcp != nil {
		cvaCondition := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.ClusterVersionAvailable))
		if cvaCondition != nil && cvaCondition.Status == metav1.ConditionTrue {
			hyperv1.SetInstallationStage(install, hyperv1.InstallationStageInstallationComplete, now)
			return
		}
	}

	// Backfill: if stage is empty and the guest ClusterVersion history on the HCP
	// already records a completed rollout, jump to InstallationComplete.
	// Use hcp.Status.VersionStatus (populated directly from the guest ClusterVersion
	// by HCCO) rather than hcluster.Status.Version, because computeClusterVersionStatus
	// can synthesize CompletedUpdate on hcluster.Status.Version from control-plane
	// release images alone for legacy HCPs without VersionStatus.
	if install.Stage == "" && hasCompletedVersionHistory(hcp) {
		hyperv1.SetInstallationStage(install, hyperv1.InstallationStageInstallationComplete, now)
		return
	}

	// Evaluate all triggers; SetInstallationStage's forward-only guard ensures
	// the highest applicable stage wins.

	// Ordinal 0: Initializing (unconditional fallback).
	hyperv1.SetInstallationStage(install, hyperv1.InstallationStageInitializing, now)

	// Ordinal 1: InfrastructureReady — HCP InfrastructureReady=True.
	if hcp != nil {
		infraCondition := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.InfrastructureReady))
		if infraCondition != nil && infraCondition.Status == metav1.ConditionTrue {
			hyperv1.SetInstallationStage(install, hyperv1.InstallationStageInfrastructureReady, now)
		}
	}

	// Ordinal 2: ControlPlaneProvisioning — at least one CPC exists.
	if controlPlaneNamespace != "" {
		cpcList := &hyperv1.ControlPlaneComponentList{}
		if err := c.List(ctx, cpcList, client.InNamespace(controlPlaneNamespace)); err != nil {
			ctrl.LoggerFrom(ctx).Error(err, "failed to list control plane components for installation stage", "namespace", controlPlaneNamespace)
		} else if len(cpcList.Items) > 0 {
			hyperv1.SetInstallationStage(install, hyperv1.InstallationStageControlPlaneProvisioning, now)
		}
	}

	// Ordinal 3: APIServerAvailable — HCP KubeAPIServerAvailable=True.
	if hcp != nil {
		kasCondition := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.KubeAPIServerAvailable))
		if kasCondition != nil && kasCondition.Status == metav1.ConditionTrue {
			hyperv1.SetInstallationStage(install, hyperv1.InstallationStageAPIServerAvailable, now)
		}
	}

	// List NodePools once for both WorkerAvailable and WaitingForOperators checks.
	// If listing fails, we cannot distinguish a cluster that expects workers from
	// a zero-worker cluster, so stop here rather than prematurely advancing to
	// WaitingForOperators.
	nodePools, err := listNodePools(ctx, c, hcluster.Namespace, hcluster.Name)
	if err != nil {
		return
	}

	// Ordinal 4: WorkerAvailable — any NodePool has ≥1 healthy machine.
	workersExpected := expectsWorkers(nodePools)
	healthyWorker := hasHealthyWorkerInNodePools(nodePools)
	transitionedToWorkerAvailable := false
	if healthyWorker {
		transitionedToWorkerAvailable = hyperv1.SetInstallationStage(install, hyperv1.InstallationStageWorkerAvailable, now)
	}

	// Ordinal 5: WaitingForOperators — KubeConfig is available AND the API
	// server is reachable. When workers are expected (any NodePool has
	// nonzero replicas or autoscaling with min > 0), we wait until at least
	// one worker is healthy and preserve the WorkerAvailable transition for
	// one reconcile so HC status and the stage gauge can observe it before
	// advancing to WaitingForOperators on the next reconcile. Zero-worker
	// clusters (no NodePools, replicas: 0, or autoscaling min: 0) skip
	// WorkerAvailable.
	if hcp != nil && hcp.Status.KubeConfig != nil {
		kasCondition := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.KubeAPIServerAvailable))
		if kasCondition != nil && kasCondition.Status == metav1.ConditionTrue {
			if !workersExpected || (healthyWorker && !transitionedToWorkerAvailable) {
				hyperv1.SetInstallationStage(install, hyperv1.InstallationStageWaitingForOperators, now)
			}
		}
	}
}

// expectsWorkers returns true if any NodePool for the cluster expects at
// least one worker node. A NodePool expects workers when it has replicas > 0,
// or autoscaling configured with min > 0. NodePools with replicas: 0,
// autoscaling min: 0 (scale-from-zero), or no replicas/autoscaling set are
// treated as not expecting workers.
func expectsWorkers(nodePools []hyperv1.NodePool) bool {
	for i := range nodePools {
		np := &nodePools[i]
		if np.Spec.Replicas != nil && *np.Spec.Replicas > 0 {
			return true
		}
		if np.Spec.AutoScaling != nil && np.Spec.AutoScaling.Min != nil && *np.Spec.AutoScaling.Min > 0 {
			return true
		}
	}
	return false
}

// hasCompletedVersionHistory returns true if the HCP's guest ClusterVersion
// status history (hcp.Status.VersionStatus) records at least one completed
// update, providing durable guest-installation completion evidence even if
// the stage field was previously empty.
func hasCompletedVersionHistory(hcp *hyperv1.HostedControlPlane) bool {
	if hcp == nil || hcp.Status.VersionStatus == nil {
		return false
	}
	for _, entry := range hcp.Status.VersionStatus.History {
		if entry.State == configv1.CompletedUpdate || entry.CompletionTime != nil {
			return true
		}
	}
	return false
}

// hasHealthyWorker returns true if any NodePool for the cluster has at least
// one ready machine, determined by checking AllMachinesReady=True or
// NodeVersion.ReadyNodeCount > 0. Both paths are guarded by
// ObservedGeneration to avoid advancing a forward-only stage based on a
// stale condition from an older spec.
func hasHealthyWorker(ctx context.Context, c client.Client, hcluster *hyperv1.HostedCluster) bool {
	nodePools, err := listNodePools(ctx, c, hcluster.Namespace, hcluster.Name)
	if err != nil {
		return false
	}
	return hasHealthyWorkerInNodePools(nodePools)
}

func hasHealthyWorkerInNodePools(nodePools []hyperv1.NodePool) bool {
	for i := range nodePools {
		np := &nodePools[i]
		// Check AllMachinesReady condition with generation guard.
		// NodePool uses NodePoolCondition (corev1.ConditionStatus), not metav1.Condition.
		if findNodePoolConditionTrueCurrent(np.Status.Conditions, hyperv1.NodePoolAllMachinesReadyConditionType, np.Generation) {
			return true
		}
		// Check NodeVersion ready counts as a fallback.
		// Guard with the same ObservedGeneration check on AllMachinesReady:
		// if the condition is stale (wrong generation), the NodeVersion counts
		// may also be stale, so skip to avoid advancing a forward-only stage.
		if isNodePoolConditionStale(np.Status.Conditions, hyperv1.NodePoolAllMachinesReadyConditionType, np.Generation) {
			continue
		}
		for _, nv := range np.Status.NodesInfo.NodeVersions {
			if nv.ReadyNodeCount != nil && *nv.ReadyNodeCount > 0 {
				return true
			}
		}
	}
	return false
}

// findNodePoolConditionTrueCurrent returns true if the named condition is True
// and its ObservedGeneration matches the expected generation. A zero
// ObservedGeneration is treated as current (older controllers may not set it).
func findNodePoolConditionTrueCurrent(conditions []hyperv1.NodePoolCondition, conditionType string, generation int64) bool {
	for i := range conditions {
		c := &conditions[i]
		if c.Type == conditionType && c.Status == corev1.ConditionTrue {
			if c.ObservedGeneration != 0 && c.ObservedGeneration != generation {
				continue
			}
			return true
		}
	}
	return false
}

// isNodePoolConditionStale returns true if the named condition exists and its
// ObservedGeneration is non-zero but does not match the expected generation,
// indicating the condition reflects an older NodePool spec.
func isNodePoolConditionStale(conditions []hyperv1.NodePoolCondition, conditionType string, generation int64) bool {
	for i := range conditions {
		c := &conditions[i]
		if c.Type == conditionType {
			return c.ObservedGeneration != 0 && c.ObservedGeneration != generation
		}
	}
	return false
}
