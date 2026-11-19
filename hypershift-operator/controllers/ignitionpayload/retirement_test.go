package ignitionpayload

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	payloadstore "github.com/openshift/hypershift/support/ignitionpayload"

	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
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
	// The retired payload is freed from the store (only current remains).
	gens, _ := r.Store.ListByOwner(ctx, owner)
	g.Expect(gens).To(ConsistOf(cr.Status.Current.RolloutGeneration))
	g.Expect(gens).ToNot(ContainElement(int64(1)))
}

func scenarioOrphanSweep(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	r, _, _ := newTestReconciler(t, coreObjs()...)
	owner := payloadstore.OwnerRef{Namespace: testNS, Name: "np-1"}

	// Inject an orphan entry (e.g. an interrupted generation that wrote before the status update).
	g.Expect(r.Store.Put(ctx, owner, 99, "orphan-id", []byte("x"))).To(Succeed())

	reconcileCR(t, r) // gen 1
	gens, _ := r.Store.ListByOwner(ctx, owner)
	g.Expect(gens).ToNot(ContainElement(int64(99)), "orphan swept")
	g.Expect(gens).To(HaveLen(1), "only the current generation remains")
}
