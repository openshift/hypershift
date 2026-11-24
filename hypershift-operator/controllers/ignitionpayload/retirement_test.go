package ignitionpayload

import (
	"context"
	"errors"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	payloadstore "github.com/openshift/hypershift/support/ignitionpayload"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func coreObjs() []client.Object {
	return []client.Object{
		testCR(), pullSecret("ps"),
		cfgMap(testNS, "user-a", mc("00-user-a")),
		cfgMap(testNS, "core-fips", mc("00-core-fips")),
		cfgMap(testNS, "haproxy", mc("00-haproxy")),
	}
}

func scenarioAddsFinalizer(t *testing.T) {
	g := NewWithT(t)
	r, c, _ := newTestReconciler(t, coreObjs()...)
	reconcileCR(t, r)
	cr := getCR(t, c)
	g.Expect(controllerutil.ContainsFinalizer(cr, storeCleanupFinalizer)).To(BeTrue())
	g.Expect(cr.Status.Current.RolloutGeneration).To(Equal(int64(1)), "first reconcile still generates")
}

func scenarioDeletionFreesPayloadsAndRemovesFinalizer(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	r, c, _ := newTestReconciler(t, coreObjs()...)
	owner := payloadstore.OwnerRef{Namespace: testNS, Name: "np-1"}

	reconcileCR(t, r) // gen 1 + finalizer
	gens, _ := r.Store.ListByOwner(ctx, owner)
	g.Expect(gens).To(HaveLen(1))

	// Delete the CR: with the finalizer it is marked for deletion, not removed.
	cr := getCR(t, c)
	g.Expect(c.Delete(ctx, cr)).To(Succeed())

	// Reconcile observes deletionTimestamp → frees store payloads + removes finalizer → CR gone.
	reconcileCR(t, r)
	gens, _ = r.Store.ListByOwner(ctx, owner)
	g.Expect(gens).To(BeEmpty(), "all owner payloads freed on teardown")
	err := c.Get(ctx, client.ObjectKey{Namespace: testNS, Name: "np-1"}, &hyperv1.IgnitionPayload{})
	g.Expect(apierrors.IsNotFound(err)).To(BeTrue(), "CR gone after finalizer removed")
}

func scenarioRetirementClearsPrevious(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	r, c, _ := newTestReconciler(t, coreObjs()...)
	owner := payloadstore.OwnerRef{Namespace: testNS, Name: "np-1"}

	reconcileCR(t, r) // gen 1
	updateCM(t, c, "user-a", mc("00-user-a-v2"))
	reconcileCR(t, r) // gen 2: previous = gen 1
	cr := getCR(t, c)
	g.Expect(cr.Status.Previous.RolloutGeneration).To(Equal(int64(1)))

	// Signal that generation 1 has drained.
	cr.Spec.RetiredRolloutGeneration = 1
	g.Expect(c.Update(ctx, cr)).To(Succeed())

	reconcileCR(t, r)
	cr = getCR(t, c)
	g.Expect(cr.Status.Previous.RolloutGeneration).To(Equal(int64(0)), "previous cleared once retired")
	g.Expect(cr.Status.Previous.UserDataSecret.Name).To(BeEmpty())
	// The next reconcile frees the retired payload from the store (only current remains).
	reconcileCR(t, r)
	gens, _ := r.Store.ListByOwner(ctx, owner)
	g.Expect(gens).To(ConsistOf(cr.Status.Current.RolloutGeneration))
	g.Expect(gens).ToNot(ContainElement(int64(1)))
}

// userDataExists reports whether the named userData Secret is present.
func userDataExists(t *testing.T, c client.Client, name string) bool {
	t.Helper()
	err := c.Get(context.Background(), client.ObjectKey{Namespace: testNS, Name: name}, &corev1.Secret{})
	if apierrors.IsNotFound(err) {
		return false
	}
	NewWithT(t).Expect(err).ToNot(HaveOccurred())
	return true
}

// scenarioRenderFailureKeepsPrevious: a rollout whose render fails must not touch the generations
// status still advertises. Nodes booting on previous keep being served and the draining MachineSet's
// userData Secret survives; the old previous is evicted only once the new generation is committed.
func scenarioRenderFailureKeepsPrevious(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	r, c, fp := newTestReconciler(t, coreObjs()...)
	owner := payloadstore.OwnerRef{Namespace: testNS, Name: "np-1"}

	reconcileCR(t, r) // gen 1
	updateCM(t, c, "user-a", mc("00-user-a-v2"))
	reconcileCR(t, r) // gen 2: previous = gen 1

	// A rollout-triggering change whose render fails, repeatedly.
	fp.err = errors.New("render failed")
	updateCM(t, c, "user-a", mc("00-user-a-v3"))
	for range 3 {
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKey{Namespace: testNS, Name: "np-1"}})
		g.Expect(err).To(MatchError(ContainSubstring("render failed")))
	}

	cr := getCR(t, c)
	g.Expect(cr.Status.Current.RolloutGeneration).To(Equal(int64(2)))
	g.Expect(cr.Status.Previous.RolloutGeneration).To(Equal(int64(1)))
	gens, err := r.Store.ListByOwner(ctx, owner)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(gens).To(ConsistOf(int64(1), int64(2)), "previous payload retained while the next render fails")
	g.Expect(userDataExists(t, c, "np-1-1")).To(BeTrue(), "previous userData retained while the next render fails")
	g.Expect(userDataExists(t, c, "np-1-2")).To(BeTrue())

	// Render recovers → gen 3 commits, and only then (on the next reconcile's reclaim) is gen 1 evicted.
	fp.err = nil
	reconcileCR(t, r)
	cr = getCR(t, c)
	g.Expect(cr.Status.Current.RolloutGeneration).To(Equal(int64(3)))
	g.Expect(cr.Status.Previous.RolloutGeneration).To(Equal(int64(2)))
	reconcileCR(t, r)
	gens, err = r.Store.ListByOwner(ctx, owner)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(gens).To(ConsistOf(int64(2), int64(3)))
	g.Expect(userDataExists(t, c, "np-1-1")).To(BeFalse(), "evicted userData reclaimed after commit")
	g.Expect(userDataExists(t, c, "np-1-2")).To(BeTrue())
	g.Expect(userDataExists(t, c, "np-1-3")).To(BeTrue())
}
