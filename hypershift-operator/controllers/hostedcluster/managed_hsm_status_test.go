package hostedcluster

import (
	"context"
	"errors"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/api/util/ipnet"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests/controlplaneoperator"
	"github.com/openshift/hypershift/pkg/manifests"
	"github.com/openshift/hypershift/support/api"
	fakecapabilities "github.com/openshift/hypershift/support/capabilities/fake"
	"github.com/openshift/hypershift/support/releaseinfo"
	"github.com/openshift/hypershift/support/releaseinfo/testutils"
	"github.com/openshift/hypershift/support/thirdparty/library-go/pkg/image/dockerv1client"
	"github.com/openshift/hypershift/support/upsert"
	"github.com/openshift/hypershift/support/util/fakeimagemetadataprovider"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clocktesting "k8s.io/utils/clock/testing"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"github.com/blang/semver"
	"go.uber.org/mock/gomock"
)

func managedHSMHostedCluster() *hyperv1.HostedCluster {
	hc := credentialHostedCluster()
	hc.Finalizers = []string{HostedClusterFinalizer}
	hc.Spec.ClusterID = "12345678-1234-1234-1234-123456789abc"
	hc.Spec.InfraID = "managed-hsm-test"
	hc.Spec.Platform = hyperv1.PlatformSpec{Type: hyperv1.AzurePlatform, Azure: &hyperv1.AzurePlatformSpec{Topology: hyperv1.AzureTopologyPublic}}
	hc.Spec.SecretEncryption = &hyperv1.SecretEncryptionSpec{Type: hyperv1.KMS, KMS: &hyperv1.KMSSpec{Azure: &hyperv1.AzureKMSSpec{KeyVaultType: hyperv1.AzureKMSKeyVaultTypeManagedHSM}}}
	hc.Spec.Networking.ClusterNetwork = []hyperv1.ClusterNetworkEntry{{CIDR: *ipnet.MustParseCIDR("172.16.0.0/16")}}
	hc.Spec.Networking.ServiceNetwork = []hyperv1.ServiceNetworkEntry{{CIDR: *ipnet.MustParseCIDR("172.31.0.0/16")}}
	hc.Spec.Services = []hyperv1.ServicePublishingStrategyMapping{{Service: hyperv1.Ignition, ServicePublishingStrategy: hyperv1.ServicePublishingStrategy{Type: hyperv1.Route}}}
	hc.Spec.Release.Image = "quay.io/openshift-release-dev/ocp-release:4.21.3"
	hc.Spec.PullSecret.Name = "pull-secret"
	return hc
}

func testManagedHSMBeforeInitialStatus(t *testing.T) {
	t.Helper()
	g := NewWithT(t)
	hc := managedHSMHostedCluster()
	pullSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: hc.Namespace, Name: hc.Spec.PullSecret.Name}, Data: map[string][]byte{".dockerconfigjson": []byte("{}")}}
	statusWrites := 0
	c := fake.NewClientBuilder().WithScheme(api.Scheme).WithStatusSubresource(hc).WithObjects(hc, pullSecret).WithInterceptorFuncs(interceptor.Funcs{
		SubResourceUpdate: func(ctx context.Context, c client.Client, subresource string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
			if target, ok := obj.(*hyperv1.HostedCluster); ok {
				condition := meta.FindStatusCondition(target.Status.Conditions, string(hyperv1.ValidHostedClusterConfiguration))
				g.Expect(condition).NotTo(BeNil())
				g.Expect(condition.Status).To(Equal(metav1.ConditionFalse), "the first status write must include the Managed HSM result")
				statusWrites++
			}
			return c.SubResource(subresource).Update(ctx, obj, opts...)
		},
		Create: func(context.Context, client.WithWatch, client.Object, ...client.CreateOption) error {
			return errors.New("downstream creation must be blocked by configuration validation")
		},
	}).Build()
	provider := releaseinfo.NewMockProviderWithOpenShiftImageRegistryOverrides(gomock.NewController(t))
	provider.EXPECT().Lookup(gomock.Any(), gomock.Any(), gomock.Any()).Return(testutils.InitReleaseImageOrDie("4.21.3"), nil).AnyTimes()
	r := &HostedClusterReconciler{Client: c, Clock: clocktesting.NewFakeClock(time.Now()), CertRotationScale: 24 * time.Hour,
		createOrUpdate: func(ctrl.Request) upsert.CreateOrUpdateFN { return ctrl.CreateOrUpdate }, ManagementClusterCapabilities: &fakecapabilities.FakeSupportAllCapabilities{},
		RegistryProvider: fakeReleaseProvider{releaseProvider: provider, metadataProvider: fakeimagemetadataprovider.FakeRegistryClientImageMetadataProvider{Result: &dockerv1client.DockerImageConfig{Architecture: "amd64"}}}, now: metav1.Now,
	}
	var transitionTime metav1.Time
	for i := 0; i < 2; i++ {
		_, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(hc)})
		g.Expect(err).To(MatchError(ContainSubstring("configuration is invalid")))
		g.Expect(err).To(MatchError(ContainSubstring("does not support Azure Managed HSM")))
		g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(hc), hc)).To(Succeed())
		condition := meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.ValidHostedClusterConfiguration))
		g.Expect(condition.Status).To(Equal(metav1.ConditionFalse))
		g.Expect(condition.ObservedGeneration).To(Equal(int64(4)))
		if i == 0 {
			transitionTime = condition.LastTransitionTime
		} else {
			g.Expect(condition.LastTransitionTime).To(Equal(transitionTime))
		}
	}
	g.Expect(statusWrites).To(Equal(2))
}

func testManagedHSMDegradedReconciliation(t *testing.T, missingPullSecret bool) {
	t.Helper()
	g := NewWithT(t)
	hc := managedHSMHostedCluster()
	hc.Spec.NodeSelector = map[string]string{"node-role.kubernetes.io/worker": ""}
	namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: manifests.HostedControlPlaneNamespace(hc.Namespace, hc.Name)}}
	hcp := controlplaneoperator.HostedControlPlane(namespace.Name, hc.Name)
	objects := []client.Object{hc, namespace, hcp}
	if !missingPullSecret {
		objects = append(objects, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Namespace: hc.Namespace, Name: hc.Spec.PullSecret.Name}, Data: map[string][]byte{}})
	}
	c := fake.NewClientBuilder().WithScheme(api.Scheme).WithStatusSubresource(hc).WithObjects(objects...).Build()
	provider := releaseinfo.NewMockProviderWithOpenShiftImageRegistryOverrides(gomock.NewController(t))
	provider.EXPECT().Lookup(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errors.New("release unavailable")).AnyTimes()
	r := &HostedClusterReconciler{Client: c, Clock: clocktesting.NewFakeClock(time.Now()), CertRotationScale: 24 * time.Hour,
		createOrUpdate: func(ctrl.Request) upsert.CreateOrUpdateFN { return ctrl.CreateOrUpdate }, ManagementClusterCapabilities: &fakecapabilities.FakeSupportAllCapabilities{},
		RegistryProvider: fakeReleaseProvider{releaseProvider: provider, metadataProvider: fakeimagemetadataprovider.FakeRegistryClientImageMetadataProvider{Result: &dockerv1client.DockerImageConfig{Architecture: "amd64"}}}, now: metav1.Now,
	}
	_, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(hc)})
	g.Expect(err).To(HaveOccurred())
	g.Expect(err.Error()).NotTo(ContainSubstring("configuration validation is incomplete"))
	g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(hc), hc)).To(Succeed())
	condition := meta.FindStatusCondition(hc.Status.Conditions, string(hyperv1.ValidHostedClusterConfiguration))
	g.Expect(condition).NotTo(BeNil())
	g.Expect(condition.Status).To(Equal(metav1.ConditionUnknown))
	g.Expect(condition.ObservedGeneration).To(Equal(hc.Generation))
	g.Expect(c.Get(t.Context(), client.ObjectKeyFromObject(hcp), hcp)).To(Succeed())
	g.Expect(hcp.Spec.NodeSelector).To(Equal(hc.Spec.NodeSelector))
	g.Expect(hcp.Spec.ReleaseImage).To(Equal(hc.Spec.Release.Image))
	g.Expect(hcp.Spec.SecretEncryption).To(Equal(hc.Spec.SecretEncryption))
}

func TestComputeValidHostedClusterConfiguration(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name       string
		mutate     func(*hyperv1.HostedCluster)
		version    string
		versionErr error
		want       metav1.ConditionStatus
		message    string
	}{
		{name: "When Managed HSM is unsupported, it should reject the configuration before persistence", version: "4.21.3", want: metav1.ConditionFalse, message: "does not support Azure Managed HSM"},
		{name: "When Managed HSM is supported, it should accept the configuration", version: "4.22.0", want: metav1.ConditionTrue},
		{name: "When the Managed HSM release is unavailable, it should not claim configuration success", versionErr: errors.New("release unavailable"), want: metav1.ConditionUnknown},
		{name: "When the Managed HSM release version is malformed, it should leave configuration validation incomplete", versionErr: errors.New("failed to parse release version"), want: metav1.ConditionUnknown},
		{name: "When Azure uses KeyVault, it should retain version-independent validation", mutate: func(hc *hyperv1.HostedCluster) {
			hc.Spec.SecretEncryption.KMS.Azure.KeyVaultType = hyperv1.AzureKMSKeyVaultTypeKeyVault
		}, versionErr: errors.New("release unavailable"), want: metav1.ConditionTrue},
		{name: "When encryption is disabled, it should retain version-independent validation", mutate: func(hc *hyperv1.HostedCluster) { hc.Spec.SecretEncryption = nil }, versionErr: errors.New("release unavailable"), want: metav1.ConditionTrue},
		{name: "When the platform is not Azure, it should retain version-independent validation", mutate: func(hc *hyperv1.HostedCluster) { hc.Spec.Platform = hyperv1.PlatformSpec{Type: hyperv1.NonePlatform} }, versionErr: errors.New("release unavailable"), want: metav1.ConditionTrue},
		{name: "When other configuration is invalid, it should preserve that rejection even with a supported HSM version", mutate: func(hc *hyperv1.HostedCluster) { hc.Spec.ClusterID = "invalid" }, version: "4.22.0", want: metav1.ConditionFalse, message: "cannot parse cluster ID"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			hc := managedHSMHostedCluster()
			if tc.mutate != nil {
				tc.mutate(hc)
			}
			version := semver.Version{}
			if tc.version != "" {
				version = semver.MustParse(tc.version)
			}
			r := &HostedClusterReconciler{Client: fake.NewClientBuilder().WithScheme(api.Scheme).Build(), ManagementClusterCapabilities: &fakecapabilities.FakeSupportAllCapabilities{}}
			condition := r.computeValidHostedClusterConfiguration(t.Context(), hc, version, tc.versionErr)
			g.Expect(condition.Status).To(Equal(tc.want))
			g.Expect(condition.ObservedGeneration).To(Equal(hc.Generation))
			if tc.message != "" {
				g.Expect(condition.Message).To(ContainSubstring(tc.message))
			}
		})
	}
}
