package etcd

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// ReconcilePodDisruptionBudget reconciles the PodDisruptionBudget for a specific etcd shard
// This function modifies the PDB in place and should be used with createOrUpdate pattern
func ReconcilePodDisruptionBudget(
	pdb *policyv1.PodDisruptionBudget,
	hcp *hyperv1.HostedControlPlane,
	shard hyperv1.ManagedEtcdShardSpec,
) error {
	// Set minimum available replicas to maintain quorum
	// For a 3-node etcd cluster, we need at least 2 available (quorum = n/2 + 1)
	minAvailable := intstr.FromInt(1)
	pdb.Spec.MinAvailable = &minAvailable

	// Update selector to match shard-specific pods
	if pdb.Spec.Selector == nil {
		pdb.Spec.Selector = &metav1.LabelSelector{}
	}
	if pdb.Spec.Selector.MatchLabels == nil {
		pdb.Spec.Selector.MatchLabels = make(map[string]string)
	}
	pdb.Spec.Selector.MatchLabels["app"] = "etcd"
	pdb.Spec.Selector.MatchLabels["hypershift.openshift.io/etcd-shard"] = shard.Name

	return nil
}
