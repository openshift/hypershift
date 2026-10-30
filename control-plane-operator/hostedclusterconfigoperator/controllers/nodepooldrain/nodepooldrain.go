package nodepooldrain

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/go-logr/logr"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/pdbdrain"
	"github.com/openshift/hypershift/support/statuspatching"

	"github.com/prometheus/client_golang/prometheus"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"

	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	// resyncInterval is a backstop for PDB status churn the watches may under-trigger,
	// and refreshes the up-to-date signal.
	// Pod placement changes are intentionally not watched to avoid caching every guest Pod.
	// Recompute frequently enough to bound how long a move between pools can leave the signal stale.
	resyncInterval = time.Minute
	// staleAfter is the grace window before declaring the signal stale on repeated guest-read failures.
	staleAfter = pdbdrain.SignalMaxAge
	// maxPodsPerPDB bounds the (uncached) pod list per PDB so a pathological namespace cannot spike
	// HCCO load. A truncated list invalidates the whole probe rather than publishing partial data.
	maxPodsPerPDB = 2000

	reasonBlocked   = "BlockedByPodDisruptionBudget"
	reasonDrainable = "Drainable"
)

var errPodScanIncomplete = errors.New("pod scan exceeded the per-PDB limit")

// drainBlockedGauge is sparse: a series exists only while a pool is blocked (set to 1), and is
// deleted when the pool is drainable again — so the fleet-wide alert is a simple `== 1` and the
// metric does not emit a zero series per nodepool per cluster.
var drainBlockedGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
	Name: "hypershift_nodepool_drain_blocked",
	Help: "Present and set to 1 only while a NodePool's nodes cannot be drained due to a customer PodDisruptionBudget.",
}, []string{"namespace", "nodepool"})

// drainUpToDateGauge is 1 when drain readiness was freshly computed, 0 when the signal is stale
// (guest unreachable past the grace window) — i.e. the gate is failing open and blind.
var drainUpToDateGauge = prometheus.NewGaugeVec(prometheus.GaugeOpts{
	Name: "hypershift_nodepool_drain_uptodate",
	Help: "1 when NodePool drain readiness was freshly computed from the guest, 0 when stale (gate failing open).",
}, []string{"namespace"})

func init() {
	metrics.Registry.MustRegister(drainBlockedGauge, drainUpToDateGauge)
}

// blockInfo carries the (name-bearing) detail for one blocking PDB, used only for in-guest Events
// and never persisted to HCP status or written to management-cluster logs.
type blockInfo struct {
	pdb   *policyv1.PodDisruptionBudget
	pools []string
}

type reconciler struct {
	mgtClusterClient   crclient.Client
	guestClient        crclient.Client
	guestAPIReader     crclient.Reader
	recorder           record.EventRecorder
	hcpNamespace       string
	hcpName            string
	platformNSPrefixes []string

	mu           sync.Mutex
	lastSuccess  time.Time
	lastBlocking map[string]map[string]bool // PDB key -> pools previously reported blocked
}

func (r *reconciler) Reconcile(ctx context.Context, _ reconcile.Request) (reconcile.Result, error) {
	log := ctrl.LoggerFrom(ctx)
	hcp := &hyperv1.HostedControlPlane{ObjectMeta: metav1.ObjectMeta{Namespace: r.hcpNamespace, Name: r.hcpName}}

	drain, blocking, err := r.computeDrain(ctx)
	if err != nil {
		r.mu.Lock()
		stale := time.Since(r.lastSuccess) > staleAfter
		r.mu.Unlock()
		incomplete := errors.Is(err, errPodScanIncomplete)
		if incomplete {
			stale = true
		}
		if stale {
			drainUpToDateGauge.WithLabelValues(r.hcpNamespace).Set(0)
			drainBlockedGauge.DeletePartialMatch(prometheus.Labels{"namespace": r.hcpNamespace})
			reason := "GuestUnreachable"
			message := fmt.Sprintf("drain readiness has not refreshed for more than %s; check HCCO logs", staleAfter)
			if incomplete {
				reason = "PodScanIncomplete"
				message = fmt.Sprintf("drain readiness scan exceeded the %d-pod safety limit; check HCCO logs", maxPodsPerPDB)
			}
			if patchErr := statuspatching.PatchStatusCondition(ctx, r.mgtClusterClient, hcp, &hcp.Status.Conditions, metav1.Condition{
				Type:    hyperv1.HostedControlPlaneNodePoolDrainUpToDate,
				Status:  metav1.ConditionFalse,
				Reason:  reason,
				Message: message,
			}); patchErr != nil {
				return reconcile.Result{}, fmt.Errorf("mark nodepool drain readiness unverified: %w", patchErr)
			}
		}
		if incomplete {
			log.Error(err, "nodepool drain readiness scan incomplete")
			return reconcile.Result{RequeueAfter: resyncInterval}, nil
		}
		return reconcile.Result{}, fmt.Errorf("compute nodepool drain: %w", err)
	}

	probeTime := metav1.Now()
	if err := statuspatching.PatchStatus(ctx, r.mgtClusterClient, hcp, func() error {
		hcp.Status.NodePoolDrain = drain
		hcp.Status.NodePoolDrainLastProbeTime = &probeTime
		return nil
	}); err != nil {
		return reconcile.Result{}, fmt.Errorf("patch nodepool drain status: %w", err)
	}
	if err := statuspatching.PatchStatusCondition(ctx, r.mgtClusterClient, hcp, &hcp.Status.Conditions, metav1.Condition{
		Type:   hyperv1.HostedControlPlaneNodePoolDrainUpToDate,
		Status: metav1.ConditionTrue,
		Reason: hyperv1.AsExpectedReason,
	}); err != nil {
		return reconcile.Result{}, fmt.Errorf("patch up-to-date condition: %w", err)
	}

	r.emitTransitions(log, blocking)
	r.updateMetrics(drain)
	drainUpToDateGauge.WithLabelValues(r.hcpNamespace).Set(1)

	r.mu.Lock()
	r.lastSuccess = time.Now()
	r.mu.Unlock()
	log.V(1).Info("reconciled nodepool drain readiness", "pools", len(drain))
	return reconcile.Result{RequeueAfter: resyncInterval}, nil
}

// computeDrain probes the guest and returns per-NodePool drain readiness (name-free, for HCP status)
// plus blocking detail (with names, for in-guest Events only), keyed by namespace/name.
func (r *reconciler) computeDrain(ctx context.Context) ([]hyperv1.NodePoolDrain, map[string]blockInfo, error) {
	var nodes corev1.NodeList
	if err := r.guestClient.List(ctx, &nodes); err != nil {
		return nil, nil, fmt.Errorf("list nodes: %w", err)
	}
	nodeToPool := map[string]string{}
	pools := map[string]bool{}
	for i := range nodes.Items {
		if pool := nodes.Items[i].Labels[hyperv1.NodePoolLabel]; pool != "" {
			nodeToPool[nodes.Items[i].Name] = pool
			pools[pool] = true
		}
	}

	var pdbs policyv1.PodDisruptionBudgetList
	if err := r.guestClient.List(ctx, &pdbs); err != nil {
		return nil, nil, fmt.Errorf("list pdbs: %w", err)
	}

	blockingCount := map[string]int32{}
	blocking := map[string]blockInfo{}
	for i := range pdbs.Items {
		pdb := &pdbs.Items[i]
		if pdbdrain.IsPlatformNamespace(pdb.Namespace, r.platformNSPrefixes) || !pdbdrain.IsStructurallyBlocking(pdb) {
			continue
		}
		if pdb.Spec.Selector == nil { // nil selector matches nothing (LabelSelectorAsSelector semantics)
			continue
		}
		sel, err := metav1.LabelSelectorAsSelector(pdb.Spec.Selector) // empty {} selector -> Everything (intended)
		if err != nil {
			continue
		}

		// Uncached, server-side filtered, bounded pod list — no cluster-wide informer, bounded cost.
		var pods corev1.PodList
		if err := r.guestAPIReader.List(ctx, &pods,
			crclient.InNamespace(pdb.Namespace), crclient.MatchingLabelsSelector{Selector: sel},
			crclient.Limit(maxPodsPerPDB)); err != nil {
			return nil, nil, errors.New("list pods for a blocking PDB failed")
		}
		if pods.Continue != "" || len(pods.Items) > maxPodsPerPDB {
			return nil, nil, errPodScanIncomplete
		}

		affected := map[string]bool{}
		for j := range pods.Items {
			pod := &pods.Items[j]
			if !pdbdrain.IsPDBBlockingPod(pdb, pod) {
				continue
			}
			if pool, ok := nodeToPool[pod.Spec.NodeName]; ok {
				affected[pool] = true
			}
		}
		if len(affected) == 0 {
			continue
		}
		poolList := make([]string, 0, len(affected))
		for pool := range affected {
			blockingCount[pool]++
			poolList = append(poolList, pool)
		}
		sort.Strings(poolList)
		blocking[pdb.Namespace+"/"+pdb.Name] = blockInfo{pdb: pdb, pools: poolList}
	}

	out := make([]hyperv1.NodePoolDrain, 0, len(pools))
	for pool := range pools {
		n := blockingCount[pool]
		blocked := n > 0
		e := hyperv1.NodePoolDrain{NodePoolName: pool, State: hyperv1.NodePoolDrainStateDrainable, Reason: reasonDrainable}
		if blocked {
			e.BlockingCount = ptr.To(n)
			e.State = hyperv1.NodePoolDrainStateBlocked
			e.Reason = reasonBlocked
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NodePoolName < out[j].NodePoolName })
	return out, blocking, nil
}

// emitTransitions records a guest Event when a PDB newly blocks a pool. Customer workload names
// stay in the guest Event; management-cluster logs contain pool names only. The recorder resolves
// the involvedObject GVK via the manager scheme.
func (r *reconciler) emitTransitions(log logr.Logger, blocking map[string]blockInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	keys := make([]string, 0, len(blocking))
	for key := range blocking {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		info := blocking[key]
		previousPools := r.lastBlocking[key]
		newPools := make([]string, 0, len(info.pools))
		for _, pool := range info.pools {
			if !previousPools[pool] {
				newPools = append(newPools, pool)
			}
		}
		if len(newPools) > 0 {
			r.recorder.Eventf(info.pdb, corev1.EventTypeWarning, "DrainBlocked",
				"PodDisruptionBudget %s prevents draining NodePool(s) %v during cluster upgrades; relax minAvailable/maxUnavailable or scale the workload", key, newPools)
			log.Info("nodepool drain blocked by a customer PodDisruptionBudget", "nodepools", newPools)
		}
	}
	next := make(map[string]map[string]bool, len(blocking))
	for key, info := range blocking {
		pools := make(map[string]bool, len(info.pools))
		for _, pool := range info.pools {
			pools[pool] = true
		}
		next[key] = pools
	}
	r.lastBlocking = next
}

// updateMetrics keeps the blocked gauge sparse: only blocked pools get a series; cleared pools are deleted.
func (r *reconciler) updateMetrics(drain []hyperv1.NodePoolDrain) {
	drainBlockedGauge.DeletePartialMatch(prometheus.Labels{"namespace": r.hcpNamespace})
	for _, e := range drain {
		if e.State == hyperv1.NodePoolDrainStateBlocked {
			drainBlockedGauge.WithLabelValues(r.hcpNamespace, e.NodePoolName).Set(1)
		}
	}
}
