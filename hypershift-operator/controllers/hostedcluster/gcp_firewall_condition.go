package hostedcluster

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// gcpFirewallRulesReadyCondition guards against propagating a stale
// GCPFirewallRulesReady condition onto the HostedCluster.
//
// HostedCluster.Generation and HostedControlPlane.Generation are independent
// counters. The generic condition-copy loop that calls this helper stamps
// every copied condition's ObservedGeneration with the HostedCluster's own
// generation, which says nothing about whether the HCP's condition is fresh
// relative to the HCP's own current spec. If the HCP spec changes (e.g. the
// effective NodePort range) before the control-plane-operator re-reconciles,
// the previous True result would otherwise be re-stamped as if it reflected
// the new spec.
//
// Report Unknown/pending instead whenever the HCP condition's own
// ObservedGeneration trails the HCP's current generation.
func gcpFirewallRulesReadyCondition(hcp *hyperv1.HostedControlPlane, hcpCondition *metav1.Condition) *metav1.Condition {
	if hcp == nil || hcpCondition == nil || hcpCondition.ObservedGeneration == hcp.Generation {
		return hcpCondition
	}
	return &metav1.Condition{
		Type:   hcpCondition.Type,
		Status: metav1.ConditionUnknown,
		Reason: hyperv1.StatusUnknownReason,
		Message: fmt.Sprintf(
			"Waiting for the hosted control plane to re-evaluate GCP firewall rules at generation %d (last observed at generation %d)",
			hcp.Generation, hcpCondition.ObservedGeneration,
		),
	}
}
