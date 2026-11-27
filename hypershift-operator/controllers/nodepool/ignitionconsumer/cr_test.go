package ignitionconsumer

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/sets"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func testNodePool(name string) *hyperv1.NodePool {
	return &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "clusters"},
		Spec:       hyperv1.NodePoolSpec{Release: hyperv1.Release{Image: "quay.io/openshift/release:4.23"}},
	}
}

func TestIgnitionPayloadSpec(t *testing.T) {
	g := NewWithT(t)

	nodePool := testNodePool("np-1")
	hc := &hyperv1.HostedCluster{
		Spec: hyperv1.HostedClusterSpec{
			PullSecret:            corev1.LocalObjectReference{Name: "my-pull-secret"},
			AdditionalTrustBundle: &corev1.LocalObjectReference{Name: "my-trust-bundle"},
		},
	}
	rolloutRefs := []hyperv1.ConfigMapReference{{Name: "a"}, {Name: "b"}}
	mgmtRefs := []hyperv1.ConfigMapReference{{Name: "haproxy"}}

	spec := ignitionPayloadSpec(nodePool, hc, rolloutRefs, mgmtRefs, "rollout-global", "rhel-9", "private=false,port=6443")

	g.Expect(spec.ReleaseImage).To(Equal("quay.io/openshift/release:4.23"))
	// The CR lives in the HCP namespace, so it references the control plane's copies under their
	// fixed names, not the HostedCluster's own Secret/ConfigMap names.
	g.Expect(spec.PullSecret.Name).To(Equal("pull-secret"))
	g.Expect(spec.AdditionalTrustBundle.Name).To(Equal("user-ca-bundle"))
	g.Expect(spec.OSImageStream.Name).To(Equal("rhel-9"))
	g.Expect(spec.RolloutGlobalConfig.Name).To(Equal("rollout-global"))
	// The endpoint identity is carried as an opaque rollout input (not in a ConfigMap), so an
	// endpoint change rolls even though the HAProxy config stays in managementConfigMaps.
	g.Expect(spec.AdditionalRolloutInputs).To(Equal("private=false,port=6443"))
	g.Expect(spec.RolloutConfigMaps).To(Equal(rolloutRefs))
	g.Expect(spec.ManagementConfigMaps).To(Equal(mgmtRefs))

	// A hosted cluster without an additional trust bundle leaves the field unset.
	hc.Spec.AdditionalTrustBundle = nil
	spec = ignitionPayloadSpec(nodePool, hc, rolloutRefs, mgmtRefs, "rollout-global", "rhel-9", "")
	g.Expect(spec.AdditionalTrustBundle.Name).To(BeEmpty())

	// An empty osStream leaves OSImageStream unset so the generator defaults it by release version.
	spec = ignitionPayloadSpec(nodePool, hc, rolloutRefs, mgmtRefs, "rollout-global", "", "")
	g.Expect(spec.OSImageStream.Name).To(BeEmpty())
}

func TestReconcileIgnitionPayloadCR(t *testing.T) {
	g := NewWithT(t)
	ctx := context.Background()
	const hcpNS = "hcp"
	nodePool := testNodePool("np-1")

	c := fake.NewClientBuilder().WithScheme(api.Scheme).Build()

	spec := hyperv1.IgnitionPayloadSpec{ReleaseImage: "img-1", PullSecret: hyperv1.SecretReference{Name: "ps"}}
	cr, err := reconcileIgnitionPayloadCR(ctx, c, hcpNS, nodePool, spec)
	g.Expect(err).ToNot(HaveOccurred())

	// Created in the HCP namespace, named after the NodePool, with the back-reference annotation
	// (ns/name) for the reverse watch, the NodePool label, and the consumer finalizer.
	g.Expect(cr.Name).To(Equal("np-1"))
	g.Expect(cr.Namespace).To(Equal(hcpNS))
	g.Expect(cr.Annotations[nodePoolAnnotation]).To(Equal("clusters/np-1"))
	g.Expect(cr.Labels[hyperv1.NodePoolLabel]).To(Equal("np-1"))
	g.Expect(sets.New(cr.Finalizers...).Has(consumerFinalizer)).To(BeTrue())
	g.Expect(cr.Spec.ReleaseImage).To(Equal("img-1"))

	// A second reconcile with a new spec updates in place (no duplicate) and must preserve
	// retiredRolloutGeneration, which is owned by AdvanceRetiredRolloutGeneration, not the builder.
	fetched := &hyperv1.IgnitionPayload{}
	g.Expect(c.Get(ctx, client.ObjectKey{Namespace: hcpNS, Name: "np-1"}, fetched)).To(Succeed())
	fetched.Spec.RetiredRolloutGeneration = 3
	g.Expect(c.Update(ctx, fetched)).To(Succeed())

	spec2 := hyperv1.IgnitionPayloadSpec{ReleaseImage: "img-2", PullSecret: hyperv1.SecretReference{Name: "ps"}}
	cr2, err := reconcileIgnitionPayloadCR(ctx, c, hcpNS, nodePool, spec2)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(cr2.Spec.ReleaseImage).To(Equal("img-2"))
	g.Expect(cr2.Spec.RetiredRolloutGeneration).To(Equal(int64(3)))

	list := &hyperv1.IgnitionPayloadList{}
	g.Expect(c.List(ctx, list, client.InNamespace(hcpNS))).To(Succeed())
	g.Expect(list.Items).To(HaveLen(1))
}
