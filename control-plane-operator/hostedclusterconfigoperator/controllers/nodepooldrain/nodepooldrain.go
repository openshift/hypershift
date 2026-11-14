package nodepooldrain

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/pdbdrain"
	"github.com/openshift/hypershift/support/statuspatching"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/record"
	"k8s.io/utils/ptr"

	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	// resyncInterval is a backstop for PDB status churn the watches may under-trigger,
	// and refreshes the up-to-date signal.
	// Pod placement changes are intentionally not watched to avoid caching every guest Pod.
	// Recompute frequently enough to bound how long a move between pools can leave the signal stale.
	resyncInterval = time.Minute
	// staleAfter is the grace window before declaring the signal stale on repeated guest-read failures.
	staleAfter = pdbdrain.SignalMaxAge
	// Pod lists are uncached. Bound both each list and the total cost of a probe so many matching
	// PDBs cannot create unbounded guest API work.
	maxPodsPerPDB              = 2000
	maxPodsPerProbe            = 10000
	maxPodListRequestsPerProbe = 100
	podListTimeout             = 30 * time.Second
	maxPodScanTime             = 2 * time.Minute

	reasonBlocked   = "BlockedByPodDisruptionBudget"
	reasonDrainable = "Drainable"
)

var errPodScanIncomplete = errors.New("pod scan exceeded a safety limit")
var errPDBStatusUnobserved = errors.New("PDB status has not observed its current spec")

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

// blockInfo carries pool names for in-memory transition deduplication. PDB keys remain private to
// the reconciler and are never written to events, status, or logs.
type blockInfo struct {
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

	probeStartedAt := metav1.Now()
	drain, blocking, err := r.computeDrain(ctx)
	if err != nil {
		r.mu.Lock()
		stale := time.Since(r.lastSuccess) > staleAfter
		r.mu.Unlock()
		podScanIncomplete := errors.Is(err, errPodScanIncomplete)
		pdbStatusUnobserved := errors.Is(err, errPDBStatusUnobserved)
		incomplete := podScanIncomplete || pdbStatusUnobserved
		if incomplete {
			stale = true
		}
		if stale {
			drainUpToDateGauge.WithLabelValues(r.hcpNamespace).Set(0)
			drainBlockedGauge.DeletePartialMatch(prometheus.Labels{"namespace": r.hcpNamespace})
			reason := "GuestUnreachable"
			message := fmt.Sprintf("drain readiness has not refreshed for more than %s; check HCCO logs", staleAfter)
			if podScanIncomplete {
				reason = "PodScanIncomplete"
				message = fmt.Sprintf("drain readiness scan exceeded a safety limit (%d pods total, %d per PDB, %d lists); check HCCO logs", maxPodsPerProbe, maxPodsPerPDB, maxPodListRequestsPerProbe)
			} else if pdbStatusUnobserved {
				reason = "PDBStatusUnobserved"
				message = "drain readiness could not verify the current PodDisruptionBudget status; check HCCO logs"
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

	if err := statuspatching.PatchStatus(ctx, r.mgtClusterClient, hcp, func() error {
		hcp.Status.NodePoolDrain = drain
		// Store the scan start time for freshness checks. Per-entry UIDs identify the
		// NodePool incarnation independently of clocks and pool names.
		hcp.Status.NodePoolDrainLastProbeTime = &probeStartedAt
		return nil
	}); err != nil {
		drainUpToDateGauge.WithLabelValues(r.hcpNamespace).Set(0)
		drainBlockedGauge.DeletePartialMatch(prometheus.Labels{"namespace": r.hcpNamespace})
		conditionErr := statuspatching.PatchStatusCondition(ctx, r.mgtClusterClient, hcp, &hcp.Status.Conditions, metav1.Condition{
			Type:    hyperv1.HostedControlPlaneNodePoolDrainUpToDate,
			Status:  metav1.ConditionFalse,
			Reason:  "StatusWriteFailed",
			Message: "nodepool drain readiness could not be published",
		})
		if conditionErr != nil {
			return reconcile.Result{}, fmt.Errorf("patch nodepool drain status and mark it unverified: %w", errors.Join(err, conditionErr))
		}
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
// plus private blocking keys for transition deduplication.
func (r *reconciler) computeDrain(ctx context.Context) ([]hyperv1.NodePoolDrain, map[string]blockInfo, error) {
	scanCtx, cancelScan := context.WithTimeout(ctx, maxPodScanTime)
	defer cancelScan()

	var nodes corev1.NodeList
	if err := r.guestClient.List(scanCtx, &nodes); err != nil {
		return nil, nil, classifyScanErr(ctx, scanCtx, err, "list nodes")
	}
	var machines capiv1.MachineList
	if err := r.mgtClusterClient.List(scanCtx, &machines, crclient.InNamespace(r.hcpNamespace)); err != nil {
		return nil, nil, classifyScanErr(ctx, scanCtx, err, "list machines for drain readiness")
	}
	poolUIDs, nodeToPool := r.resolveVerifiedPools(&nodes, &machines)

	var pdbs policyv1.PodDisruptionBudgetList
	if err := r.guestClient.List(scanCtx, &pdbs); err != nil {
		return nil, nil, classifyScanErr(ctx, scanCtx, err, "list pdbs")
	}
	blockingCount, blocking, err := r.scanBlockingPDBs(ctx, scanCtx, &pdbs, nodeToPool)
	if err != nil {
		return nil, nil, err
	}

	out := make([]hyperv1.NodePoolDrain, 0, len(poolUIDs))
	for pool, uid := range poolUIDs {
		n := blockingCount[pool]
		blocked := n > 0
		e := hyperv1.NodePoolDrain{NodePoolName: pool, NodePoolUID: uid, State: hyperv1.NodePoolDrainStateDrainable, Reason: reasonDrainable}
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

// classifyScanErr maps a guest/management list error during a probe to a shutdown
// error (parent ctx canceled), the incomplete-scan sentinel (scan deadline hit), or
// a wrapped generic error.
func classifyScanErr(ctx, scanCtx context.Context, err error, what string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(scanCtx.Err(), context.DeadlineExceeded) {
		return errPodScanIncomplete
	}
	return fmt.Errorf("%s: %w", what, err)
}

// resolveVerifiedPools builds, from guest Nodes cross-checked against their CAPI
// Machines, the set of pools whose Node membership is unambiguous (returned in
// poolUIDs) and a node-name -> pool map restricted to those pools. A pool with any
// unlabeled, orphaned, differently identified, or UID-conflicting Node is dropped,
// including old Nodes left behind by a deleted-and-recreated pool of the same name.
func (r *reconciler) resolveVerifiedPools(nodes *corev1.NodeList, machines *capiv1.MachineList) (map[string]string, map[string]string) {
	machineByName := make(map[string]*capiv1.Machine, len(machines.Items))
	for i := range machines.Items {
		machineByName[machines.Items[i].Name] = &machines.Items[i]
	}
	poolUIDs := map[string]string{}
	unverifiedPools := map[string]bool{}
	for i := range nodes.Items {
		node := &nodes.Items[i]
		pool := node.Labels[hyperv1.NodePoolLabel]
		if pool == "" {
			continue
		}
		machine := machineByName[node.Annotations[capiv1.MachineAnnotation]]
		if !r.nodeMatchesPool(node, machine) {
			unverifiedPools[pool] = true
			continue
		}
		uid := machine.Annotations[hyperv1.NodePoolUIDAnnotation]
		if previous := poolUIDs[pool]; previous != "" && previous != uid {
			unverifiedPools[pool] = true
		} else {
			poolUIDs[pool] = uid
		}
	}
	for pool := range unverifiedPools {
		delete(poolUIDs, pool)
	}
	nodeToPool := map[string]string{}
	for i := range nodes.Items {
		node := &nodes.Items[i]
		if pool := node.Labels[hyperv1.NodePoolLabel]; pool != "" && poolUIDs[pool] != "" {
			nodeToPool[node.Name] = pool
		}
	}
	return poolUIDs, nodeToPool
}

// nodeMatchesPool reports whether a labeled guest Node is backed by a CAPI Machine
// that unambiguously belongs to the same NodePool incarnation in this HCP namespace.
func (r *reconciler) nodeMatchesPool(node *corev1.Node, machine *capiv1.Machine) bool {
	pool := node.Labels[hyperv1.NodePoolLabel]
	if node.Annotations[capiv1.ClusterNamespaceAnnotation] != r.hcpNamespace ||
		machine == nil ||
		node.Annotations[capiv1.MachineAnnotation] == "" ||
		machine.Annotations[hyperv1.NodePoolLabel] == "" ||
		!strings.HasSuffix(machine.Annotations[hyperv1.NodePoolLabel], "/"+pool) ||
		machine.Annotations[hyperv1.NodePoolUIDAnnotation] == "" ||
		(machine.Status.NodeRef.IsDefined() && machine.Status.NodeRef.Name != node.Name) {
		return false
	}
	return true
}

// scanBlockingPDBs lists customer Pods for each structurally-blocking PDB and counts,
// per pool, how many such PDBs would block a drain. It returns the blocking counts and
// the per-PDB affected-pool map (private, for transition dedup), or an error that marks
// the whole probe incomplete/unverified.
func (r *reconciler) scanBlockingPDBs(ctx, scanCtx context.Context, pdbs *policyv1.PodDisruptionBudgetList, nodeToPool map[string]string) (map[string]int32, map[string]blockInfo, error) {
	blockingCount := map[string]int32{}
	blocking := map[string]blockInfo{}
	listedPods := 0
	podListRequests := 0
	for i := range pdbs.Items {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		if scanCtx.Err() != nil {
			return nil, nil, errPodScanIncomplete
		}
		pdb := &pdbs.Items[i]
		sel, keep, err := structuralSelector(pdb, r.platformNSPrefixes)
		if err != nil {
			return nil, nil, err
		}
		if !keep {
			continue
		}
		pods, err := r.listBlockingPods(ctx, scanCtx, pdb, sel, &listedPods, &podListRequests)
		if err != nil {
			return nil, nil, err
		}
		affected := affectedPools(pdb, pods, nodeToPool)
		if len(affected) == 0 {
			continue
		}
		for _, pool := range affected {
			blockingCount[pool]++
		}
		blocking[pdb.Namespace+"/"+pdb.Name] = blockInfo{pools: affected}
	}
	if ctx.Err() != nil {
		return nil, nil, ctx.Err()
	}
	if scanCtx.Err() != nil {
		return nil, nil, errPodScanIncomplete
	}
	return blockingCount, blocking, nil
}

// structuralSelector decides whether a PDB is in scope for the drain check and returns
// its pod selector when it is. keep is false for platform namespaces, nil selectors, and
// PDBs that are not structurally blocking. Only an unobserved PDB whose reported status
// still looks structurally blocking returns errPDBStatusUnobserved: it is decision-relevant
// and cannot be trusted, so publishing a result would risk a false Drainable. An unobserved
// non-structural PDB is skipped and re-evaluated on the next probe once its status settles
// (the same generation-change watch requeues us), so an unrelated customer PDB edit does not
// mark every pool Unknown, at the cost of a <=1 probe recall gap on a PDB turning structural.
func structuralSelector(pdb *policyv1.PodDisruptionBudget, prefixes []string) (labels.Selector, bool, error) {
	if pdbdrain.IsPlatformNamespace(pdb.Namespace, prefixes) {
		return nil, false, nil
	}
	if pdb.Spec.Selector == nil { // nil selector matches nothing (LabelSelectorAsSelector semantics)
		return nil, false, nil
	}
	structurallyBlocking := pdbdrain.StructurallyBlockingFromStatus(pdb)
	if pdb.Status.ObservedGeneration < pdb.Generation {
		if !structurallyBlocking {
			return nil, false, nil
		}
		return nil, false, errPDBStatusUnobserved
	}
	if !structurallyBlocking {
		return nil, false, nil
	}
	sel, err := metav1.LabelSelectorAsSelector(pdb.Spec.Selector) // empty {} selector -> Everything (intended)
	if err != nil {
		//nolint:nilerr // a malformed selector means this PDB matches nothing we can act on; skip it rather than failing the whole scan (matches prior behavior).
		return nil, false, nil
	}
	return sel, true, nil
}

// listBlockingPods performs the uncached, server-side filtered, bounded Pod list for one
// PDB, enforcing the per-PDB and per-probe caps. It advances the running counters and
// returns errPodScanIncomplete if any safety limit or timeout is hit.
func (r *reconciler) listBlockingPods(ctx, scanCtx context.Context, pdb *policyv1.PodDisruptionBudget, sel labels.Selector, listedPods, podListRequests *int) ([]corev1.Pod, error) {
	remaining := maxPodsPerProbe - *listedPods
	if remaining <= 0 || *podListRequests >= maxPodListRequestsPerProbe {
		return nil, errPodScanIncomplete
	}
	*podListRequests++
	listLimit := min(maxPodsPerPDB, remaining)
	var pods corev1.PodList
	podCtx, cancelPodList := context.WithTimeout(scanCtx, podListTimeout)
	listErr := r.guestAPIReader.List(podCtx, &pods,
		crclient.InNamespace(pdb.Namespace), crclient.MatchingLabelsSelector{Selector: sel},
		crclient.Limit(int64(listLimit)))
	podListTimedOut := errors.Is(podCtx.Err(), context.DeadlineExceeded)
	cancelPodList()
	if listErr != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(listErr, context.DeadlineExceeded) || podListTimedOut || errors.Is(scanCtx.Err(), context.DeadlineExceeded) {
			return nil, errPodScanIncomplete
		}
		return nil, errors.New("list pods for a blocking PDB failed")
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if podListTimedOut || scanCtx.Err() != nil {
		return nil, errPodScanIncomplete
	}
	if pods.Continue != "" || len(pods.Items) > listLimit {
		return nil, errPodScanIncomplete
	}
	*listedPods += len(pods.Items)
	return pods.Items, nil
}

// affectedPools returns the sorted set of pools with at least one Pod this PDB would
// block from eviction. It counts each PDB once per pool regardless of Pod count.
func affectedPools(pdb *policyv1.PodDisruptionBudget, pods []corev1.Pod, nodeToPool map[string]string) []string {
	affected := map[string]bool{}
	for j := range pods {
		pod := &pods[j]
		if !pdbdrain.IsPDBBlockingPod(pdb, pod) {
			continue
		}
		if pool, ok := nodeToPool[pod.Spec.NodeName]; ok {
			affected[pool] = true
		}
	}
	if len(affected) == 0 {
		return nil
	}
	poolList := make([]string, 0, len(affected))
	for pool := range affected {
		poolList = append(poolList, pool)
	}
	sort.Strings(poolList)
	return poolList
}

// emitTransitions records a generic guest Event when a PDB newly blocks a pool. PDB and NodePool
// names are kept out of event objects, event messages, and management-cluster logs.
func (r *reconciler) emitTransitions(log logr.Logger, blocking map[string]blockInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	keys := make([]string, 0, len(blocking))
	for key := range blocking {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	newlyBlockedPools := 0
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
			newlyBlockedPools += len(newPools)
		}
	}
	if newlyBlockedPools > 0 {
		genericEventObject := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: metav1.NamespaceDefault}}
		r.recorder.Eventf(genericEventObject, corev1.EventTypeWarning, "DrainBlocked",
			"One or more PodDisruptionBudgets may block worker node drain during an upgrade.")
		log.Info("nodepool drain readiness is blocked", "count", newlyBlockedPools, "reason", reasonBlocked)
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
