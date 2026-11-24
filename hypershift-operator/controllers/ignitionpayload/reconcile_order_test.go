package ignitionpayload

import (
	"context"
	"errors"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	payloadstore "github.com/openshift/hypershift/support/ignitionpayload"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// TestReconciler_OrderOfOperations covers the reconcile ordering contract:
//   - status decisions are made on an authoritative (uncached) read of the CR;
//   - reclaim (sweep + retirement) runs first, independent of the generation inputs;
//   - the sweep keeps current, previous, and anything above current (uncommitted), and frees the rest;
//   - every status commit is guarded against a re-read that is behind (stale cache: retried) or ahead
//     (concurrent commit: aborted) of the generation the reconcile decided on;
//   - the refresh path repairs a stored current payload that diverges from status.
func TestReconciler_OrderOfOperations(t *testing.T) {
	t.Run("When the rollout config reverts while the informer lags, it should not reclaim the committed current generation", scenarioStaleRevertKeepsCurrent)
	t.Run("When the rollout config changes while the informer lags, it should not overwrite the committed current generation", scenarioStaleRolloutKeepsCurrent)
	t.Run("When retirement is committed while the informer lags, it should still clear previous", scenarioStaleRetireStillCommits)
	t.Run("When status.current advanced concurrently, it should abort the commit without overwriting it", scenarioConcurrentAdvanceAborts)
	t.Run("When the config inputs are unavailable, it should still retire previous and reclaim orphans", scenarioReclaimIndependentOfInputs)
	t.Run("When an interrupted rollout left the next generation uncommitted, it should keep it and reuse it without re-rendering", scenarioUncommittedNextGenerationReused)
	t.Run("When a generation below current is unreferenced, it should reclaim its payload and only CR-controlled generation Secrets", scenarioReclaimBelowCurrent)
	t.Run("When the stored current payload diverges from status, it should re-render it", scenarioRefreshRepairsDivergedStore)
}

var testOwner = payloadstore.OwnerRef{Namespace: testNS, Name: "np-1"}

func reconcileErr(r *Reconciler) error {
	_, err := r.Reconcile(context.Background(), reconcile.Request{NamespacedName: client.ObjectKey{Namespace: testNS, Name: "np-1"}})
	return err
}

func storedGens(t *testing.T, r *Reconciler) []int64 {
	t.Helper()
	gens, err := r.Store.ListByOwner(context.Background(), testOwner)
	NewWithT(t).Expect(err).ToNot(HaveOccurred())
	return gens
}

// cachedView makes the reconciler's cached client serve view(call, live) for IgnitionPayload Gets,
// where call counts those Gets from 1 and live is the API's current object. A nil view passes through.
// The uncached APIReader stays the fake client itself, as in production.
func cachedView(t *testing.T, r *Reconciler, c client.Client, view func(call int, live *hyperv1.IgnitionPayload) *hyperv1.IgnitionPayload) {
	t.Helper()
	calls := 0
	r.Client = interceptor.NewClient(c.(client.WithWatch), interceptor.Funcs{
		Get: func(ctx context.Context, wc client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			cr, ok := obj.(*hyperv1.IgnitionPayload)
			if !ok {
				return wc.Get(ctx, key, obj, opts...)
			}
			calls++
			live := &hyperv1.IgnitionPayload{}
			if err := wc.Get(ctx, key, live, opts...); err != nil {
				return err
			}
			if v := view(calls, live); v != nil {
				v.DeepCopyInto(cr)
				return nil
			}
			live.DeepCopyInto(cr)
			return nil
		},
	})
}

// staleFor serves snapshot for the first n cached IgnitionPayload Gets, then catches up — an
// informer that lags the API for a bounded time, as it does in production.
func staleFor(n int, snapshot *hyperv1.IgnitionPayload) func(int, *hyperv1.IgnitionPayload) *hyperv1.IgnitionPayload {
	return func(call int, _ *hyperv1.IgnitionPayload) *hyperv1.IgnitionPayload {
		if call <= n {
			return snapshot
		}
		return nil
	}
}

// rollToThree drives the CR to generation 3 (user-a v1 -> v2 -> v3), returning the status snapshots
// observed after generations 1 and 2.
func rollToThree(t *testing.T, r *Reconciler, c client.Client) (afterGen1, afterGen2 *hyperv1.IgnitionPayload) {
	t.Helper()
	g := NewWithT(t)
	reconcileCR(t, r)
	afterGen1 = getCR(t, c)
	updateCM(t, c, "user-a", mc("00-user-a-v2"))
	reconcileCR(t, r)
	afterGen2 = getCR(t, c)
	updateCM(t, c, "user-a", mc("00-user-a-v3"))
	reconcileCR(t, r)
	g.Expect(getCR(t, c).Status.Current.RolloutGeneration).To(Equal(int64(3)))
	return afterGen1, afterGen2
}

func scenarioStaleRevertKeepsCurrent(t *testing.T) {
	g := NewWithT(t)
	r, c, _ := newTestReconciler(t, coreObjs()...)
	_, afterGen2 := rollToThree(t, r, c)
	cachedView(t, r, c, staleFor(2, afterGen2))

	// Revert to generation 2's rollout config. Against the stale view this is a no-op refresh of gen 2
	// whose sweep would reclaim gen 3; against the API it is a rollout to gen 4 superseding gen 3.
	updateCM(t, c, "user-a", mc("00-user-a-v2"))
	reconcileCR(t, r)

	cr := getCR(t, c)
	g.Expect(cr.Status.Current.RolloutGeneration).To(Equal(int64(4)))
	g.Expect(cr.Status.Previous.RolloutGeneration).To(Equal(int64(3)))
	g.Expect(storedGens(t, r)).To(ContainElements(int64(3), int64(4)))
	g.Expect(userDataExists(t, c, "np-1-3")).To(BeTrue())
	g.Expect(userDataExists(t, c, "np-1-4")).To(BeTrue())
}

func scenarioStaleRolloutKeepsCurrent(t *testing.T) {
	g := NewWithT(t)
	r, c, _ := newTestReconciler(t, coreObjs()...)
	_, afterGen2 := rollToThree(t, r, c)
	_, gen3Hash, err := r.Store.Get(context.Background(), testOwner, 3)
	g.Expect(err).ToNot(HaveOccurred())
	cachedView(t, r, c, staleFor(2, afterGen2))

	// A new rollout config. Against the stale view the next generation is 3 again, so the generator
	// would overwrite the committed gen 3 entry with gen 4's content; it must allocate gen 4 instead.
	updateCM(t, c, "user-a", mc("00-user-a-v4"))
	reconcileCR(t, r)

	cr := getCR(t, c)
	g.Expect(cr.Status.Current.RolloutGeneration).To(Equal(int64(4)))
	g.Expect(cr.Status.Previous.RolloutGeneration).To(Equal(int64(3)))
	_, hash, err := r.Store.Get(context.Background(), testOwner, 3)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(hash).To(Equal(gen3Hash), "committed generation 3 must not be overwritten")
}

func scenarioStaleRetireStillCommits(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	r, c, _ := newTestReconciler(t, coreObjs()...)
	reconcileCR(t, r) // gen 1
	afterGen1 := getCR(t, c)
	updateCM(t, c, "user-a", mc("00-user-a-v2"))
	reconcileCR(t, r) // gen 2, previous = 1

	cr := getCR(t, c)
	cr.Spec.RetiredRolloutGeneration = 1
	g.Expect(c.Update(ctx, cr)).To(Succeed())

	// The cache still shows generation 1 (previous empty), so a blind retire mutation would be a no-op
	// against it and silently skip the commit.
	cachedView(t, r, c, staleFor(2, afterGen1))
	reconcileCR(t, r)

	cr = getCR(t, c)
	g.Expect(cr.Status.Current.RolloutGeneration).To(Equal(int64(2)))
	g.Expect(cr.Status.Previous.RolloutGeneration).To(Equal(int64(0)), "retirement committed despite the lagging cache")
}

func scenarioConcurrentAdvanceAborts(t *testing.T) {
	g := NewWithT(t)
	r, c, _ := newTestReconciler(t, coreObjs()...)
	reconcileCR(t, r) // gen 1
	updateCM(t, c, "user-a", mc("00-user-a-v2"))
	reconcileCR(t, r) // gen 2, previous = 1

	// Every commit-time re-read reports a status.current one generation past what this reconcile
	// decided on — another writer (e.g. an overlapping leader) committed in between.
	cachedView(t, r, c, func(_ int, live *hyperv1.IgnitionPayload) *hyperv1.IgnitionPayload {
		ahead := live.DeepCopy()
		ahead.Status.Previous = live.Status.Current
		ahead.Status.Current.RolloutGeneration = live.Status.Current.RolloutGeneration + 1
		return ahead
	})
	updateCM(t, c, "user-a", mc("00-user-a-v3"))
	g.Expect(reconcileErr(r)).To(HaveOccurred(), "the commit must abort, not overwrite the newer status")

	cr := getCR(t, c)
	g.Expect(cr.Status.Current.RolloutGeneration).To(Equal(int64(2)))
	g.Expect(cr.Status.Previous.RolloutGeneration).To(Equal(int64(1)))
}

func scenarioReclaimIndependentOfInputs(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	r, c, _ := newTestReconciler(t, coreObjs()...)
	rollToThree(t, r, c) // current = 3, previous = 2; gen 1 is evicted

	cr := getCR(t, c)
	cr.Spec.RetiredRolloutGeneration = 2
	g.Expect(c.Update(ctx, cr)).To(Succeed())
	// Break a generation input: a referenced rollout ConfigMap disappears.
	g.Expect(c.Delete(ctx, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: testNS, Name: "user-a"}})).To(Succeed())

	g.Expect(reconcileErr(r)).To(HaveOccurred(), "missing input still requeues")
	cr = getCR(t, c)
	g.Expect(cr.Status.Previous.RolloutGeneration).To(Equal(int64(0)), "retirement does not wait on generation inputs")
	g.Expect(storedGens(t, r)).ToNot(ContainElement(int64(1)), "orphan reclaimed despite the broken input")

	// The next reconcile (driven in production by the retire commit's own watch event) reclaims the
	// retired generation, still with the input broken.
	g.Expect(reconcileErr(r)).To(HaveOccurred())
	g.Expect(storedGens(t, r)).To(ConsistOf(int64(3)))
	g.Expect(userDataExists(t, c, "np-1-1")).To(BeFalse())
	g.Expect(userDataExists(t, c, "np-1-2")).To(BeFalse())
	g.Expect(userDataExists(t, c, "np-1-3")).To(BeTrue())
}

func scenarioUncommittedNextGenerationReused(t *testing.T) {
	g := NewWithT(t)
	r, c, fp := newTestReconciler(t, coreObjs()...)
	reconcileCR(t, r) // gen 1

	// Interrupt a rollout to gen 2 between prepare and commit: the status patch fails once.
	failed := false
	r.Client = interceptor.NewClient(c.(client.WithWatch), interceptor.Funcs{
		SubResourcePatch: func(ctx context.Context, sc client.Client, sub string, obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
			if !failed {
				failed = true
				return errors.New("apiserver unavailable")
			}
			return sc.SubResource(sub).Patch(ctx, obj, patch, opts...)
		},
	})
	updateCM(t, c, "user-a", mc("00-user-a-v2"))
	g.Expect(reconcileErr(r)).To(MatchError(ContainSubstring("apiserver unavailable")))
	g.Expect(getCR(t, c).Status.Current.RolloutGeneration).To(Equal(int64(1)))
	rendersAfterPrepare := fp.calls

	// The config reverts, so the next reconcile is a plain refresh of gen 1. The uncommitted gen 2 sits
	// above current and must survive the sweep.
	updateCM(t, c, "user-a", mc("00-user-a"))
	reconcileCR(t, r)
	g.Expect(storedGens(t, r)).To(ContainElement(int64(2)), "uncommitted next generation kept")
	g.Expect(userDataExists(t, c, "np-1-2")).To(BeTrue(), "uncommitted next generation's userData kept")

	// The rollout comes back: gen 2 is reused as-is, with no second render.
	updateCM(t, c, "user-a", mc("00-user-a-v2"))
	reconcileCR(t, r)
	g.Expect(getCR(t, c).Status.Current.RolloutGeneration).To(Equal(int64(2)))
	g.Expect(fp.calls).To(Equal(rendersAfterPrepare), "the prepared payload is reused, not re-rendered")
}

func scenarioReclaimBelowCurrent(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	r, c, _ := newTestReconciler(t, coreObjs()...)
	rollToThree(t, r, c) // current = 3, previous = 2
	cr := getCR(t, c)

	// A CR-controlled Secret whose name is not <name>-<generation> is not a generation Secret the
	// generator authored; a Secret the CR does not control is never the generator's to delete.
	unparsable := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: testNS, Name: "np-1-extra"}}
	g.Expect(controllerutil.SetControllerReference(cr, unparsable, c.Scheme())).To(Succeed())
	g.Expect(c.Create(ctx, unparsable)).To(Succeed())
	unowned := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: testNS, Name: "np-1-0"}}
	g.Expect(c.Create(ctx, unowned)).To(Succeed())

	reconcileCR(t, r)
	g.Expect(storedGens(t, r)).To(ConsistOf(int64(2), int64(3)))
	g.Expect(userDataExists(t, c, "np-1-1")).To(BeFalse(), "unreferenced generation below current reclaimed")
	g.Expect(userDataExists(t, c, "np-1-2")).To(BeTrue(), "previous kept")
	g.Expect(userDataExists(t, c, "np-1-3")).To(BeTrue(), "current kept")
	g.Expect(userDataExists(t, c, "np-1-extra")).To(BeTrue(), "non-generation Secret left alone")
	g.Expect(userDataExists(t, c, "np-1-0")).To(BeTrue(), "Secret not controlled by the CR left alone")
	g.Expect(userDataExists(t, c, "ps")).To(BeTrue(), "pull secret untouched")
}

func scenarioRefreshRepairsDivergedStore(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	r, c, fp := newTestReconciler(t, coreObjs()...)
	reconcileCR(t, r) // gen 1
	g.Expect(fp.calls).To(Equal(1))

	// The bytes behind gen 1 no longer match what status records (e.g. a losing writer's late Put).
	g.Expect(r.Store.Put(ctx, testOwner, 1, "diverged", []byte("stale-bytes"))).To(Succeed())

	reconcileCR(t, r)
	cr := getCR(t, c)
	payload, hash, err := r.Store.Get(ctx, testOwner, 1)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(hash).To(Equal(cr.Status.Current.ConfigHash))
	g.Expect(payload).To(Equal([]byte("PAYLOAD")))
	g.Expect(fp.calls).To(Equal(2), "the diverged payload is re-rendered")
}
