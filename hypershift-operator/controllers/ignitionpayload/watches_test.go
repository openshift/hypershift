package ignitionpayload

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func crWithRefs(name string, spec hyperv1.IgnitionPayloadSpec) *hyperv1.IgnitionPayload {
	return &hyperv1.IgnitionPayload{
		ObjectMeta: metav1.ObjectMeta{Namespace: testNS, Name: name},
		Spec:       spec,
	}
}

func reqFor(name string) reconcile.Request {
	return reconcile.Request{NamespacedName: client.ObjectKey{Namespace: testNS, Name: name}}
}

func TestReconciler_EnqueueIgnitionPayloadsForConfigMap(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	crA := crWithRefs("a", hyperv1.IgnitionPayloadSpec{RolloutConfigMaps: []hyperv1.ConfigMapReference{{Name: "user-a"}}})
	crB := crWithRefs("b", hyperv1.IgnitionPayloadSpec{
		ManagementConfigMaps: []hyperv1.ConfigMapReference{{Name: "haproxy-b"}},
		RolloutGlobalConfig:  hyperv1.ConfigMapReference{Name: "gc-b"},
	})
	c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(crA, crB).Build()
	r := &Reconciler{Client: c, Namespace: testNS}

	g.Expect(r.enqueueIgnitionPayloadsForConfigMap(ctx, cfgMap(testNS, "user-a", ""))).To(ConsistOf(reqFor("a")))
	g.Expect(r.enqueueIgnitionPayloadsForConfigMap(ctx, cfgMap(testNS, "haproxy-b", ""))).To(ConsistOf(reqFor("b")))
	g.Expect(r.enqueueIgnitionPayloadsForConfigMap(ctx, cfgMap(testNS, "gc-b", ""))).To(ConsistOf(reqFor("b")))
	g.Expect(r.enqueueIgnitionPayloadsForConfigMap(ctx, cfgMap(testNS, "unrelated", ""))).To(BeEmpty())
}

func TestReconciler_EnqueueIgnitionPayloadsForSecret(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	crA := crWithRefs("a", hyperv1.IgnitionPayloadSpec{PullSecret: hyperv1.SecretReference{Name: "ps-a"}})
	crB := crWithRefs("b", hyperv1.IgnitionPayloadSpec{PullSecret: hyperv1.SecretReference{Name: "ps-b"}})
	c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(crA, crB).Build()
	r := &Reconciler{Client: c, Namespace: testNS}

	secret := func(name string) *corev1.Secret {
		return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: testNS, Name: name}}
	}
	g.Expect(r.enqueueIgnitionPayloadsForSecret(ctx, secret("ps-a"))).To(ConsistOf(reqFor("a")))
	g.Expect(r.enqueueIgnitionPayloadsForSecret(ctx, secret("ps-b"))).To(ConsistOf(reqFor("b")))
	g.Expect(r.enqueueIgnitionPayloadsForSecret(ctx, secret("nope"))).To(BeEmpty())
}
