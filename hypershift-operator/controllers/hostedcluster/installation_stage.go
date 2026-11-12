package hostedcluster

import (
	"context"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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

	// Backfill: if stage is empty and HCP is already Available=True, jump to complete.
	if hcp != nil && install.Stage == "" {
		availableCondition := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.HostedControlPlaneAvailable))
		if availableCondition != nil && availableCondition.Status == metav1.ConditionTrue {
			hyperv1.SetInstallationStage(install, hyperv1.InstallationStageInstallationComplete, now)
			return
		}
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
		if err := c.List(ctx, cpcList, client.InNamespace(controlPlaneNamespace)); err == nil && len(cpcList.Items) > 0 {
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

	// Ordinal 4: WorkerAvailable — any NodePool has ≥1 healthy machine.
	if hasHealthyWorker(ctx, c, hcluster) {
		hyperv1.SetInstallationStage(install, hyperv1.InstallationStageWorkerAvailable, now)
	}

	// Ordinal 5: WaitingForOperators — KubeConfig is available AND the API
	// server is reachable. The KubeConfig Secret is created during PKI
	// generation (before KAS is running), so checking it alone would skip
	// ordinals 3 (APIServerAvailable) and 4 (WorkerAvailable). Requiring
	// KubeAPIServerAvailable=True ensures we only advance to this stage
	// once the kubeconfig is usable, while still allowing zero-worker
	// flows to progress without a healthy worker.
	if hcp != nil && hcp.Status.KubeConfig != nil {
		kasCondition := meta.FindStatusCondition(hcp.Status.Conditions, string(hyperv1.KubeAPIServerAvailable))
		if kasCondition != nil && kasCondition.Status == metav1.ConditionTrue {
			hyperv1.SetInstallationStage(install, hyperv1.InstallationStageWaitingForOperators, now)
		}
	}
}

// hasHealthyWorker returns true if any NodePool for the cluster has at least
// one ready machine, determined by checking AllMachinesReady=True or
// NodeVersion.ReadyNodeCount > 0.
func hasHealthyWorker(ctx context.Context, c client.Client, hcluster *hyperv1.HostedCluster) bool {
	nodePools, err := listNodePools(ctx, c, hcluster.Namespace, hcluster.Name)
	if err != nil || len(nodePools) == 0 {
		return false
	}
	for i := range nodePools {
		np := &nodePools[i]
		// Check AllMachinesReady condition.
		// NodePool uses NodePoolCondition (corev1.ConditionStatus), not metav1.Condition.
		if findNodePoolConditionTrue(np.Status.Conditions, hyperv1.NodePoolAllMachinesReadyConditionType) {
			return true
		}
		// Check NodeVersion ready counts.
		for _, nv := range np.Status.NodesInfo.NodeVersions {
			if nv.ReadyNodeCount != nil && *nv.ReadyNodeCount > 0 {
				return true
			}
		}
	}
	return false
}

// findNodePoolConditionTrue returns true if the named condition is True.
func findNodePoolConditionTrue(conditions []hyperv1.NodePoolCondition, conditionType string) bool {
	for i := range conditions {
		if conditions[i].Type == conditionType && conditions[i].Status == corev1.ConditionTrue {
			return true
		}
	}
	return false
}
