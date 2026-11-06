package karpenterignition

import (
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1"
	"github.com/openshift/hypershift/api/util/ipnet"
	"github.com/openshift/hypershift/support/api"
	karpenterutil "github.com/openshift/hypershift/support/karpenter"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/releaseinfo/testutils"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	fakeimagemetadataprovider "github.com/openshift/hypershift/support/util/fakeimagemetadataprovider"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/api/image/docker10"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/go-logr/logr/testr"
	"go.uber.org/mock/gomock"
)

const trustBundleTestCMName = "user-ca-bundle"

type trustBundleTestEnv struct {
	management client.Client
	guest      client.Client
	capable    *KarpenterIgnitionReconciler
	legacy     *KarpenterIgnitionReconciler
}

func newTrustBundleTestEnv(t *testing.T, caContent string) *trustBundleTestEnv {
	t.Helper()
	mockCtrl := gomock.NewController(t)
	releaseProvider := releaseinfo.NewMockProvider(mockCtrl)
	releaseProvider.EXPECT().Lookup(gomock.Any(), gomock.Any(), gomock.Any()).
		Return(testutils.InitReleaseImageOrDie("4.17.0"), nil).AnyTimes()

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{Name: "test-hcp", Namespace: testNamespace},
		Spec: hyperv1.HostedControlPlaneSpec{
			ReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.17.0-x86_64",
			InfraID:      "test-infra",
			ClusterID:    "test-cluster-id",
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.AWSPlatform,
				AWS:  &hyperv1.AWSPlatformSpec{Region: "us-east-1"},
			},
			Networking: hyperv1.ClusterNetworking{
				ServiceNetwork: []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("172.31.0.0/16")}},
			},
			PullSecret:            corev1.LocalObjectReference{Name: "pull-secret"},
			AdditionalTrustBundle: &corev1.LocalObjectReference{Name: trustBundleTestCMName},
			AutoNode: hyperv1.AutoNode{
				Provisioner: hyperv1.ProvisionerConfig{
					Name:      hyperv1.ProvisionerKarpenter,
					Karpenter: hyperv1.KarpenterConfig{Platform: hyperv1.AWSPlatform},
				},
			},
		},
		Status: hyperv1.HostedControlPlaneStatus{
			Version: "4.17.0",
			VersionStatus: &hyperv1.ClusterVersionStatus{
				Desired: configv1.Release{Version: "4.17.0"},
				History: []configv1.UpdateHistory{{
					State:          configv1.CompletedUpdate,
					Version:        "4.17.0",
					CompletionTime: &metav1.Time{Time: time.Now()},
				}},
			},
			KubeConfig: &hyperv1.KubeconfigSecretRef{Name: "admin-kubeconfig"},
		},
	}
	objects := []client.Object{
		hcp,
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "pull-secret", Namespace: testNamespace},
			Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte(`{"auths":{}}`)},
		},
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: "admin-kubeconfig", Namespace: testNamespace},
			Data: map[string][]byte{"kubeconfig": []byte(`apiVersion: v1
clusters:
- cluster:
    server: https://api.test-cluster.example.com:6443
  name: cluster
contexts:
- context:
    cluster: cluster
    user: ""
  name: cluster
current-context: cluster
kind: Config`)},
		},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "core-ignition-config-1", Namespace: testNamespace, Labels: map[string]string{"hypershift.openshift.io/core-ignition-config": "true"}}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "core-ignition-config-2", Namespace: testNamespace, Labels: map[string]string{"hypershift.openshift.io/core-ignition-config": "true"}}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "set-karpenter-taint", Namespace: testNamespace}, Data: map[string]string{"config": ""}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "ignition-server-ca-cert", Namespace: testNamespace}, Data: map[string][]byte{"tls.crt": []byte("fake-ca-cert")}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: trustBundleTestCMName, Namespace: testNamespace}, Data: map[string]string{"ca-bundle.crt": caContent}},
	}
	management := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objects...).Build()
	guest := fake.NewClientBuilder().WithScheme(api.Scheme).
		WithObjects(&hyperkarpenterv1.OpenshiftEC2NodeClass{ObjectMeta: metav1.ObjectMeta{Name: testNodeClassName}}).
		WithStatusSubresource(&hyperkarpenterv1.OpenshiftEC2NodeClass{}).
		Build()

	newReconciler := func(capable bool) *KarpenterIgnitionReconciler {
		labels := map[string]string{"io.openshift.hypershift.control-plane-operator-skips-haproxy": "true"}
		if capable {
			labels["io.openshift.hypershift.control-plane-operator-manages.rendered-trust-bundle-validation"] = "true"
		}
		return &KarpenterIgnitionReconciler{
			ControlPlaneOperatorImage: "quay.io/hypershift/control-plane-operator:test",
			ManagementClient:          management,
			GuestClient:               guest,
			ReleaseProvider:           releaseProvider,
			VersionResolver:           &fakeVersionResolver{},
			ImageMetadataProvider: &fakeimagemetadataprovider.FakeRegistryClientImageMetadataProvider{
				Result: &dockerv1client.DockerImageConfig{Config: &docker10.DockerConfig{Labels: labels}},
			},
			HypershiftOperatorImage: "test-hypershift-operator-image",
			IgnitionEndpoint:        testIgnitionEndpoint,
			Namespace:               testNamespace,
		}
	}
	return &trustBundleTestEnv{management: management, guest: guest, capable: newReconciler(true), legacy: newReconciler(false)}
}

func (e *trustBundleTestEnv) reconcile(t *testing.T, g Gomega, r *KarpenterIgnitionReconciler) *hyperkarpenterv1.OpenshiftEC2NodeClass {
	t.Helper()
	ctx := log.IntoContext(t.Context(), testr.New(t))
	_, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKey{Name: testNodeClassName}})
	g.Expect(err).NotTo(HaveOccurred())
	nodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{}
	g.Expect(e.guest.Get(ctx, client.ObjectKey{Name: testNodeClassName}, nodeClass)).To(Succeed())
	return nodeClass
}

func (e *trustBundleTestEnv) setCA(t *testing.T, g Gomega, content string) {
	t.Helper()
	cm := &corev1.ConfigMap{}
	g.Expect(e.management.Get(t.Context(), client.ObjectKey{Namespace: testNamespace, Name: trustBundleTestCMName}, cm)).To(Succeed())
	cm.Data["ca-bundle.crt"] = content
	g.Expect(e.management.Update(t.Context(), cm)).To(Succeed())
}

func (e *trustBundleTestEnv) tokenSecret(t *testing.T, g Gomega, configVersion string) *corev1.Secret {
	t.Helper()
	nodeClass := &hyperkarpenterv1.OpenshiftEC2NodeClass{ObjectMeta: metav1.ObjectMeta{Name: testNodeClassName}}
	name := "token-" + karpenterutil.KarpenterNodePoolName(nodeClass) + "-" + configVersion
	secret := &corev1.Secret{}
	g.Expect(e.management.Get(t.Context(), client.ObjectKey{Namespace: testNamespace, Name: name}, secret)).To(Succeed())
	return secret
}

func TestReconcileTrustBundleBaseline(t *testing.T) {
	t.Run("When the CPO supports rendered trust bundle validation, it should seed the baseline and roll out only on content changes", func(t *testing.T) {
		g := NewWithT(t)
		env := newTrustBundleTestEnv(t, "ca-a")

		nodeClass := env.reconcile(t, g, env.capable)
		baseline := nodeClass.Annotations[openshiftEC2NodeClassAnnotationTrustBundleBaseline]
		versionA := nodeClass.Annotations[configVersionAnnotation]
		g.Expect(baseline).NotTo(BeEmpty(), "the trust bundle baseline should be seeded")
		g.Expect(versionA).NotTo(BeEmpty())
		g.Expect(env.tokenSecret(t, g, versionA).Data).To(HaveKey("rendered-trust-bundle-hash"))

		nodeClass = env.reconcile(t, g, env.capable)
		g.Expect(nodeClass.Annotations[configVersionAnnotation]).To(Equal(versionA), "an unchanged bundle should not roll out")

		env.setCA(t, g, "ca-b")
		nodeClass = env.reconcile(t, g, env.capable)
		versionB := nodeClass.Annotations[configVersionAnnotation]
		g.Expect(versionB).NotTo(Equal(versionA), "a CA content edit should roll out")
		g.Expect(nodeClass.Annotations[openshiftEC2NodeClassAnnotationTrustBundleBaseline]).To(Equal(baseline), "the baseline should not move")

		env.setCA(t, g, "ca-a")
		nodeClass = env.reconcile(t, g, env.capable)
		g.Expect(nodeClass.Annotations[configVersionAnnotation]).To(Equal(versionA), "reverting to the baseline content should return to the original hash")
	})

	t.Run("When the CPO gains rendered trust bundle validation after a CA edit, it should absorb the edit without a rollout", func(t *testing.T) {
		g := NewWithT(t)
		env := newTrustBundleTestEnv(t, "ca-a")

		nodeClass := env.reconcile(t, g, env.legacy)
		legacyVersion := nodeClass.Annotations[configVersionAnnotation]
		g.Expect(nodeClass.Annotations).NotTo(HaveKey(openshiftEC2NodeClassAnnotationTrustBundleBaseline), "a legacy CPO should not seed a baseline")
		g.Expect(env.tokenSecret(t, g, legacyVersion).Data).NotTo(HaveKey("rendered-trust-bundle-hash"))

		// Edit the CA while the legacy CPO is in use; the legacy hash does not track content.
		env.setCA(t, g, "ca-b")
		nodeClass = env.reconcile(t, g, env.legacy)
		g.Expect(nodeClass.Annotations[configVersionAnnotation]).To(Equal(legacyVersion))

		// Capability becomes available: the baseline absorbs the current content and the hash is preserved.
		nodeClass = env.reconcile(t, g, env.capable)
		g.Expect(nodeClass.Annotations[configVersionAnnotation]).To(Equal(legacyVersion), "seeding the baseline should not roll out")
		g.Expect(nodeClass.Annotations[openshiftEC2NodeClassAnnotationTrustBundleBaseline]).NotTo(BeEmpty())
		token := env.tokenSecret(t, g, legacyVersion)
		g.Expect(token.Data).To(HaveKey("rendered-trust-bundle-hash"), "the retained token should be refreshed for ignition-server validation")

		// A subsequent edit rolls out.
		env.setCA(t, g, "ca-c")
		nodeClass = env.reconcile(t, g, env.capable)
		g.Expect(nodeClass.Annotations[configVersionAnnotation]).NotTo(Equal(legacyVersion), "an edit after migration should roll out")
		g.Expect(strings.TrimSpace(nodeClass.Annotations[configVersionAnnotation])).NotTo(BeEmpty())
	})
}
