package nodepool

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/upsert"

	configv1 "github.com/openshift/api/config/v1"
	imagev1 "github.com/openshift/api/image/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	capiv1 "sigs.k8s.io/cluster-api/api/core/v1beta2"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/go-logr/logr"
)

// rolloutFixture exercises token maintenance, migration, propagation and status
// reconciliation across multiple controller cycles. complete simulates CAPI or
// the in-place upgrader reporting that the current target has finished.
type rolloutFixture struct {
	capi     *CAPI
	md       *capiv1.MachineDeployment
	ms       *capiv1.MachineSet
	template client.Object
}

func newRolloutFixture(t *testing.T, strategy hyperv1.UpgradeType) *rolloutFixture {
	t.Helper()
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{Name: "test-np", Namespace: "clusters", Annotations: map[string]string{}},
		Spec: hyperv1.NodePoolSpec{
			Platform:   hyperv1.NodePoolPlatform{Type: hyperv1.AWSPlatform},
			Management: hyperv1.NodePoolManagement{UpgradeType: strategy},
			Release:    hyperv1.Release{Image: "quay.io/openshift-release-dev/ocp-release:4.18.5-x86_64"},
		},
		Status: hyperv1.NodePoolStatus{Version: "4.18.5"},
	}
	token := &Token{
		CreateOrUpdateProvider: upsert.New(false),
		cpoCapabilities:        &CPOCapabilities{DecompressAndDecodeConfig: true},
		userData:               &userData{proxy: &configv1.Proxy{}, ignitionServerEndpoint: "ignition.example.com"},
		ConfigGenerator: &ConfigGenerator{
			Client:                fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
			nodePool:              nodePool,
			hostedCluster:         &hyperv1.HostedCluster{},
			controlplaneNamespace: "clusters-test",
			rolloutConfig: &rolloutConfig{
				releaseImage:        &releaseinfo.ReleaseImage{ImageStream: &imagev1.ImageStream{ObjectMeta: metav1.ObjectMeta{Name: "4.18.5"}}},
				mcoRawConfig:        "user-config-A + haproxy-A",
				rolloutMcoRawConfig: "user-config-A",
			},
		},
	}
	nodePool.Annotations[nodePoolAnnotationCurrentConfig] = token.HashWithoutVersion()
	nodePool.Annotations[nodePoolAnnotationCurrentConfigVersion] = token.Hash()
	nodePool.Annotations[nodePoolAnnotationCurrentRolloutConfig] = token.RolloutHashWithoutVersion()
	NewWithT(t).Expect(token.Reconcile(t.Context())).To(Succeed())

	template := capiv1.MachineTemplateSpec{Spec: capiv1.MachineSpec{
		Version:           token.Version(),
		Bootstrap:         capiv1.Bootstrap{DataSecretName: ptr.To(token.UserDataSecret().Name)},
		InfrastructureRef: capiv1.ContractVersionedObjectReference{Name: "template"},
	}}
	return &rolloutFixture{
		capi: &CAPI{Token: token},
		md: &capiv1.MachineDeployment{
			ObjectMeta: metav1.ObjectMeta{Name: nodePool.Name, Namespace: token.controlplaneNamespace, Generation: 1},
			Spec:       capiv1.MachineDeploymentSpec{Replicas: ptr.To(int32(3)), Template: *template.DeepCopy()},
		},
		ms: &capiv1.MachineSet{
			ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{nodePoolAnnotationCurrentConfigVersion: token.Hash()}},
			Spec:       capiv1.MachineSetSpec{Template: *template.DeepCopy()},
		},
		template: &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "template"}},
	}
}

func (f *rolloutFixture) bootstrapHash() string {
	if f.capi.nodePool.Spec.Management.UpgradeType == hyperv1.UpgradeTypeReplace {
		return extractHashFromSecretName(ptr.Deref(f.md.Spec.Template.Spec.Bootstrap.DataSecretName, ""))
	}
	return extractHashFromSecretName(ptr.Deref(f.ms.Spec.Template.Spec.Bootstrap.DataSecretName, ""))
}

func (f *rolloutFixture) reconcile(t *testing.T) bool {
	t.Helper()
	c := f.capi
	// Each controller reconcile constructs a new Token; do not carry its cached
	// effective hash into the next cycle in this fixture either.
	c.effectiveHash = ""
	c.SetDeployedBootstrapHash(f.bootstrapHash())
	NewWithT(t).Expect(c.Token.Reconcile(t.Context())).To(Succeed())
	seedRolloutAnnotation(c.nodePool, c.RolloutHashWithoutVersion())
	if c.nodePool.Spec.Management.UpgradeType == hyperv1.UpgradeTypeReplace {
		return c.propagateVersionAndTemplate(logr.Discard(), f.md, f.template)
	}
	return c.propagateVersionAndTemplateToMachineSet(logr.Discard(), f.ms, f.template)
}

func (f *rolloutFixture) complete(t *testing.T) {
	t.Helper()
	if f.capi.nodePool.Spec.Management.UpgradeType == hyperv1.UpgradeTypeReplace {
		f.md.Status = capiv1.MachineDeploymentStatus{
			Replicas: ptr.To(int32(3)), UpToDateReplicas: ptr.To(int32(3)),
			AvailableReplicas: ptr.To(int32(3)), ObservedGeneration: f.md.Generation,
		}
		f.capi.reconcileMachineDeploymentStatus(t.Context(), logr.Discard(), f.md, f.template)
		return
	}
	f.ms.Annotations[nodePoolAnnotationCurrentConfigVersion] = f.ms.Annotations[nodePoolAnnotationTargetConfigVersion]
	NewWithT(t).Expect(machineSetInPlaceRolloutIsComplete(f.ms)).To(BeTrue())
	f.capi.reconcileMachineSetStatus(logr.Discard(), f.ms, f.template)
}

func (f *rolloutFixture) expectActiveToken(t *testing.T, hash, version string) {
	t.Helper()
	g := NewWithT(t)
	token, userData := f.capi.secretsForHash(hash)
	g.Expect(f.capi.Get(t.Context(), client.ObjectKeyFromObject(token), token)).To(Succeed())
	g.Expect(token.Annotations).NotTo(HaveKey(hyperv1.IgnitionServerTokenExpirationTimestampAnnotation))
	g.Expect(string(token.Data[TokenSecretReleaseVersionKey])).To(Equal(version))
	g.Expect(f.capi.Get(t.Context(), client.ObjectKeyFromObject(userData), userData)).To(Succeed())
}

func testReconcileStatusAfterReversion(t *testing.T, strategy hyperv1.UpgradeType) {
	t.Helper()
	for _, kind := range []string{"config", "version"} {
		t.Run("When a "+kind+" reversion completes after management drift, it should record the completed payload and deliver the next request", func(t *testing.T) {
			g := NewWithT(t)
			f := newRolloutFixture(t, strategy)
			c := f.capi
			originalHash := c.Hash()
			if kind == "config" {
				c.mcoRawConfig = "user-config-B + haproxy-A"
				c.rolloutMcoRawConfig = "user-config-B"
			} else {
				c.releaseImage.ImageStream.Name = "4.18.6"
				c.nodePool.Spec.Release.Image = "quay.io/openshift-release-dev/ocp-release:4.18.6-x86_64"
			}
			g.Expect(f.reconcile(t)).To(BeTrue(), "rollout B should start")

			// Restore the original user inputs while retaining newer management content.
			c.mcoRawConfig = "user-config-A + haproxy-B"
			c.rolloutMcoRawConfig = "user-config-A"
			c.releaseImage.ImageStream.Name = "4.18.5"
			c.nodePool.Spec.Release.Image = "quay.io/openshift-release-dev/ocp-release:4.18.5-x86_64"
			revertedHash := c.Hash()
			g.Expect(f.reconcile(t)).To(BeTrue(), "reversion should propagate")
			g.Expect(f.bootstrapHash()).To(Equal(revertedHash))
			g.Expect(c.nodePool.Annotations[nodePoolAnnotationCurrentConfigVersion]).To(Equal(originalHash), "propagation is not completion")
			g.Expect(f.reconcile(t)).To(BeFalse())
			f.complete(t)
			g.Expect(c.nodePool.Annotations[nodePoolAnnotationCurrentConfigVersion]).To(Equal(revertedHash))
			g.Expect(c.nodePool.Annotations).NotTo(HaveKey(nodePoolAnnotationInProgressRolloutConfig))
			f.expectActiveToken(t, revertedHash, "4.18.5")

			if kind == "config" {
				c.mcoRawConfig = "user-config-C + haproxy-B"
				c.rolloutMcoRawConfig = "user-config-C"
			} else {
				c.releaseImage.ImageStream.Name = "4.18.7"
				c.nodePool.Spec.Release.Image = "quay.io/openshift-release-dev/ocp-release:4.18.7-x86_64"
			}
			wanted := c.Hash()
			g.Expect(f.reconcile(t)).To(BeTrue(), "the next request should propagate")
			g.Expect(f.bootstrapHash()).To(Equal(wanted))
			f.expectActiveToken(t, wanted, c.Version())
			g.Expect(f.reconcile(t)).To(BeFalse())
			f.complete(t)
			g.Expect(c.nodePool.Annotations[nodePoolAnnotationCurrentConfigVersion]).To(Equal(wanted))
			g.Expect(c.nodePool.Status.Version).To(Equal(c.Version()))
			g.Expect(f.reconcile(t)).To(BeFalse(), "completion should leave the workload stable")
			f.expectActiveToken(t, wanted, c.Version())
		})
	}
}

func testReconcileStatusWithoutBootstrap(t *testing.T, strategy hyperv1.UpgradeType) {
	t.Helper()
	for _, tc := range []struct {
		name string
		ref  *string
	}{
		{name: "When the bootstrap reference is absent, it should preserve the completed hash during management drift"},
		{name: "When the bootstrap reference is empty, it should preserve the completed hash during management drift", ref: ptr.To("")},
		{name: "When the bootstrap reference has no hash, it should preserve the completed hash during management drift", ref: ptr.To("bootstrap")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			f := newRolloutFixture(t, strategy)
			completedHash := f.capi.Hash()
			f.capi.mcoRawConfig = "user-config-A + haproxy-B"
			f.md.Spec.Template.Spec.Bootstrap.DataSecretName = tc.ref
			f.ms.Spec.Template.Spec.Bootstrap.DataSecretName = tc.ref
			f.complete(t)
			g.Expect(f.capi.nodePool.Annotations[nodePoolAnnotationCurrentConfigVersion]).To(Equal(completedHash))
		})
	}
}
