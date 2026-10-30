// Package pdbdrain holds the shared, generic predicate for deciding whether a customer
// PodDisruptionBudget would structurally prevent draining a node during a NodePool upgrade.
//
// It is intentionally free of any ROSA/managed-specific policy: it reports the *mechanism*
// (is this pod evictable by drain; does this PDB structurally forbid all disruptions). The
// customer-vs-platform classification is parameterized (namespace prefixes) so it can be
// configured per management deployment, and the enforcement policy (whether to block an
// upgrade) lives in the consumer (Clusters Service), not here.
//
// This package is vendored by both the HyperShift operator and Clusters Service so the
// signal and the gate cannot drift (MCS-1421).
package pdbdrain

import (
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
)

// SignalMaxAge is the maximum age of an HCCO drain-readiness probe that consumers
// may treat as current. Both the writer and readers use this bound so a stopped
// writer cannot leave a stale NodePoolDrainUpToDate=True signal indefinitely.
const SignalMaxAge = 15 * time.Minute

// AdditionalPlatformNamespacePrefixesEnv extends the built-in platform namespace
// exclusions with a comma-separated list of prefixes for a managed deployment.
const AdditionalPlatformNamespacePrefixesEnv = "NODEPOOL_DRAIN_ADDITIONAL_PLATFORM_NAMESPACE_PREFIXES"

// DefaultPlatformNamespacePrefixes are the OpenShift-core namespaces excluded by default.
// Managed deployments may extend this (e.g. "redhat-", "open-cluster-management") via config.
var DefaultPlatformNamespacePrefixes = []string{"openshift-", "kube-"}

// PlatformNamespacePrefixes returns the built-in prefixes plus configured
// additions. Empty entries are ignored so a trailing comma cannot match every
// namespace.
func PlatformNamespacePrefixes(additional string) []string {
	prefixes := append([]string(nil), DefaultPlatformNamespacePrefixes...)
	for _, raw := range strings.Split(additional, ",") {
		prefix := strings.TrimSpace(raw)
		if prefix == "" {
			continue
		}
		found := false
		for _, existing := range prefixes {
			if existing == prefix {
				found = true
				break
			}
		}
		if !found {
			prefixes = append(prefixes, prefix)
		}
	}
	return prefixes
}

// IsStructurallyBlocking reports whether a PDB forbids ALL voluntary disruptions even when
// every selected pod is healthy (desiredHealthy >= expectedPods). Such a PDB will stall a
// drain indefinitely regardless of workload health. Precision over recall: transiently
// blocking PDBs (e.g. minAvailable with some pods already unhealthy) are deliberately NOT
// flagged here — those are the PodDisruptionBudgetLimitSRE alert's domain.
func IsStructurallyBlocking(pdb *policyv1.PodDisruptionBudget) bool {
	return pdb.Status.ExpectedPods >= 1 && pdb.Status.DesiredHealthy >= pdb.Status.ExpectedPods
}

// IsPlatformNamespace reports whether ns is a platform/managed namespace (by prefix or exact
// match) and therefore out of scope for a customer drain-readiness gate.
func IsPlatformNamespace(ns string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(ns, p) {
			return true
		}
	}
	return ns == "openshift" || ns == "kube-system"
}

// IsDrainCandidate reports whether `kubectl drain` as the HCCO drainer runs it (Force,
// IgnoreAllDaemonSets, DeleteEmptyDirData) would actually EVICT this pod. The precheck must
// model exactly the drainer's eviction set, or it predicts stalls that never happen:
// DaemonSet-owned, mirror/static, completed, and unscheduled pods are never evicted.
func IsDrainCandidate(pod *corev1.Pod) bool {
	if pod.Spec.NodeName == "" {
		return false
	}
	if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
		return false
	}
	if _, ok := pod.Annotations[corev1.MirrorPodAnnotationKey]; ok {
		return false
	}
	for _, o := range pod.OwnerReferences {
		if o.Kind == "DaemonSet" && o.Controller != nil && *o.Controller {
			return false
		}
	}
	return true
}

// IsPDBBlockingPod reports whether this PDB would prevent eviction of the pod during drain.
// Pending pods can always be evicted. For Running pods, AlwaysAllow permits eviction when the
// pod is not Ready; Ready pods remain subject to the PDB budget.
func IsPDBBlockingPod(pdb *policyv1.PodDisruptionBudget, pod *corev1.Pod) bool {
	if !IsDrainCandidate(pod) {
		return false
	}
	if pod.Status.Phase == corev1.PodPending {
		return false
	}
	if pod.Status.Phase == corev1.PodRunning &&
		pdb.Spec.UnhealthyPodEvictionPolicy != nil &&
		*pdb.Spec.UnhealthyPodEvictionPolicy == policyv1.AlwaysAllow &&
		!podReady(pod) {
		return false
	}
	return true
}

func podReady(pod *corev1.Pod) bool {
	for _, condition := range pod.Status.Conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}
