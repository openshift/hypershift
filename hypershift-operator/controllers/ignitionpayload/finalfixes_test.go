package ignitionpayload

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	supportutil "github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

// The pull-secret hash handed to GetPayload must equal HashSimple of the raw []byte of
// .dockerconfigjson (GetPayload recomputes HashSimple(pullSecretBytes)); the stringified form does
// not match.
func scenarioPullSecretHashContract(t *testing.T) {
	g := NewWithT(t)
	r, _, fp := newTestReconciler(t, coreObjs()...)
	reconcileCR(t, r)
	g.Expect(fp.lastPullSecretHash).To(Equal(supportutil.HashSimple([]byte("{}"))))
	g.Expect(fp.lastPullSecretHash).ToNot(Equal(supportutil.HashSimple("{}")))
}

func trustBundleCM(name, caBundle string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: testNS, Name: name},
		Data:       map[string]string{"ca-bundle.crt": caBundle},
	}
}

// The trust bundle is read from key "ca-bundle.crt"; its content feeds the payload-identity hash, so
// rotating it refreshes the payload behind the current generation (RF#2 at the reconcile level)
// without rolling, and the hash passed to GetPayload matches its contract.
func scenarioTrustBundleRotationMovesIdentity(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	cr := testCR()
	cr.Spec.AdditionalTrustBundle = hyperv1.ConfigMapReference{Name: "tb"}
	objs := append(coreObjs(), trustBundleCM("tb", "v1"))
	objs[0] = cr // replace the default CR with the trust-bundle-referencing one
	r, c, fp := newTestReconciler(t, objs...)

	reconcileCR(t, r)
	first := getCR(t, c)
	g.Expect(first.Status.Current.RolloutGeneration).To(Equal(int64(1)))
	userData := first.Status.Current.UserDataSecret.Name
	g.Expect(fp.calls).To(Equal(1))
	g.Expect(fp.lastTrustBundleHash).To(Equal(supportutil.HashSimple([]byte("v1"))))

	// Rotate the trust bundle content: identity changes (not rollout) → Policy-A refresh.
	tb := &corev1.ConfigMap{}
	g.Expect(c.Get(ctx, client.ObjectKey{Namespace: testNS, Name: "tb"}, tb)).To(Succeed())
	tb.Data["ca-bundle.crt"] = "v2"
	g.Expect(c.Update(ctx, tb)).To(Succeed())

	reconcileCR(t, r)
	after := getCR(t, c)
	g.Expect(after.Status.Current.RolloutGeneration).To(Equal(int64(1)), "trust-bundle rotation must not roll")
	g.Expect(after.Status.Current.UserDataSecret.Name).To(Equal(userData), "same userData Secret (Policy A)")
	g.Expect(fp.calls).To(Equal(2), "rotation triggers a refresh render")
	g.Expect(fp.lastTrustBundleHash).To(Equal(supportutil.HashSimple([]byte("v2"))))
}

// A transient missing ConfigMap must requeue (return an error), not flip the CR into a
// permanent-looking InvalidConfig state.
func scenarioMissingConfigMapRequeues(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	cr := testCR()
	cr.Spec.RolloutConfigMaps = []hyperv1.ConfigMapReference{{Name: "ghost"}} // not created
	cr.Spec.ManagementConfigMaps = nil
	r, c, _ := newTestReconciler(t, cr, pullSecret("ps"))

	_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: client.ObjectKey{Namespace: testNS, Name: "np-1"}})
	g.Expect(err).To(HaveOccurred(), "missing configmap should requeue, not be a permanent failure")

	got := getCR(t, c)
	cond := apimeta.FindStatusCondition(got.Status.Conditions, "PayloadGenerated")
	if cond != nil {
		g.Expect(cond.Reason).ToNot(Equal("InvalidConfig"), "a missing configmap is not an InvalidConfig")
	}
}
