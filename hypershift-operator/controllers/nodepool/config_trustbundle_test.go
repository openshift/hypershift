package nodepool

import (
	"context"
	"errors"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/releaseinfo"
	supportutil "github.com/openshift/hypershift/support/util"

	configv1 "github.com/openshift/api/config/v1"
	imageapi "github.com/openshift/api/image/v1"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	ctrl "sigs.k8s.io/controller-runtime"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	trustBundleTestNamespace = "test"
	trustBundleTestCPNS      = "test-test"
)

func trustBundleTestReleaseImage() *releaseinfo.ReleaseImage {
	return &releaseinfo.ReleaseImage{
		ImageStream: &imageapi.ImageStream{
			ObjectMeta: metav1.ObjectMeta{Name: "4.18.0"},
		},
	}
}

func trustBundleTestHostedCluster(additionalTrustBundle, proxyTrustedCA string) *hyperv1.HostedCluster {
	hc := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: trustBundleTestNamespace},
		Spec: hyperv1.HostedClusterSpec{
			PullSecret: corev1.LocalObjectReference{Name: "pull-secret"},
		},
	}
	if additionalTrustBundle != "" {
		hc.Spec.AdditionalTrustBundle = &corev1.LocalObjectReference{Name: additionalTrustBundle}
	}
	if proxyTrustedCA != "" {
		hc.Spec.Configuration = &hyperv1.ClusterConfiguration{
			Proxy: &configv1.ProxySpec{
				TrustedCA: configv1.ConfigMapNameReference{Name: proxyTrustedCA},
			},
		}
	}
	return hc
}

func trustBundleConfigMap(name, content string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: trustBundleTestNamespace},
		Data:       map[string]string{certs.UserCABundleMapKey: content},
	}
}

func trustBundleTestNodePool(baseline string) *hyperv1.NodePool {
	np := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{Name: "np", Namespace: trustBundleTestNamespace},
		Spec:       hyperv1.NodePoolSpec{ClusterName: "test"},
	}
	if baseline != "" {
		np.Annotations = map[string]string{nodePoolAnnotationTrustBundleBaseline: baseline}
	}
	return np
}

func newTrustBundleTestConfigGenerator(t *testing.T, hc *hyperv1.HostedCluster, np *hyperv1.NodePool, capable bool, objects ...crclient.Object) (*ConfigGenerator, error) {
	t.Helper()
	objs := append([]crclient.Object{}, coreConfigMaps...)
	objs = append(objs, objects...)
	c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(objs...).Build()
	return NewConfigGenerator(t.Context(), c, hc, np, trustBundleTestReleaseImage(), "", trustBundleTestCPNS, StreamRHEL9, capable)
}

func TestNewConfigGeneratorTrustBundleHash(t *testing.T) {
	hc := trustBundleTestHostedCluster("user-ca", "proxy-ca")
	contentHash := func(additional, proxy string) string {
		return supportutil.HashSimple("additional:" + supportutil.HashSimple(additional) + ",proxy:" + supportutil.HashSimple(proxy))
	}

	// The legacy name-based hash is what existing NodePools were rolled out with.
	legacy, err := newTrustBundleTestConfigGenerator(t, hc, trustBundleTestNodePool(""), false,
		trustBundleConfigMap("user-ca", "ca-a"), trustBundleConfigMap("proxy-ca", "proxy-a"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	testCases := []struct {
		name                  string
		capable               bool
		baseline              string
		additional            string
		proxy                 string
		expectLegacyHash      bool
		expectContentHash     string
		expectRenderedHash    string
		expectRolloutContains bool
	}{
		{
			name:             "When the CPO does not support rendered trust bundle validation, it should keep the legacy hash and not track content",
			capable:          false,
			additional:       "ca-b",
			proxy:            "proxy-b",
			expectLegacyHash: true,
		},
		{
			name:               "When the CPO is capable and no baseline is recorded, it should keep the legacy hash",
			capable:            true,
			additional:         "ca-b",
			proxy:              "proxy-a",
			expectLegacyHash:   true,
			expectContentHash:  contentHash("ca-b", "proxy-a"),
			expectRenderedHash: supportutil.HashSimple("ca-b" + "proxy-a"),
		},
		{
			name:               "When the content matches the baseline, it should keep the legacy hash",
			capable:            true,
			baseline:           contentHash("ca-a", "proxy-a"),
			additional:         "ca-a",
			proxy:              "proxy-a",
			expectLegacyHash:   true,
			expectContentHash:  contentHash("ca-a", "proxy-a"),
			expectRenderedHash: supportutil.HashSimple("ca-a" + "proxy-a"),
		},
		{
			name:                  "When the additional trust bundle content diverges from the baseline, it should change the hash",
			capable:               true,
			baseline:              contentHash("ca-a", "proxy-a"),
			additional:            "ca-b",
			proxy:                 "proxy-a",
			expectContentHash:     contentHash("ca-b", "proxy-a"),
			expectRenderedHash:    supportutil.HashSimple("ca-b" + "proxy-a"),
			expectRolloutContains: true,
		},
		{
			name:                  "When the proxy trustedCA content diverges from the baseline, it should change the hash",
			capable:               true,
			baseline:              contentHash("ca-a", "proxy-a"),
			additional:            "ca-a",
			proxy:                 "proxy-b",
			expectContentHash:     contentHash("ca-a", "proxy-b"),
			expectRenderedHash:    supportutil.HashSimple("ca-a" + "proxy-b"),
			expectRolloutContains: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			cg, err := newTrustBundleTestConfigGenerator(t, hc, trustBundleTestNodePool(tc.baseline), tc.capable,
				trustBundleConfigMap("user-ca", tc.additional), trustBundleConfigMap("proxy-ca", tc.proxy))
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(cg.TrustBundleContentHash()).To(Equal(tc.expectContentHash))
			g.Expect(cg.renderedTrustBundleHash).To(Equal(tc.expectRenderedHash))
			if tc.expectLegacyHash {
				g.Expect(cg.Hash()).To(Equal(legacy.Hash()))
				g.Expect(cg.HashWithoutVersion()).To(Equal(legacy.HashWithoutVersion()))
				g.Expect(cg.trustBundleRolloutInput).To(BeEmpty())
			} else {
				g.Expect(cg.Hash()).ToNot(Equal(legacy.Hash()))
				g.Expect(cg.HashWithoutVersion()).ToNot(Equal(legacy.HashWithoutVersion()))
			}
			if tc.expectRolloutContains {
				g.Expect(cg.trustBundleRolloutInput).To(Equal(tc.expectContentHash))
			}
		})
	}
}

func TestNewConfigGeneratorTrustBundleEditSequence(t *testing.T) {
	g := NewWithT(t)
	hc := trustBundleTestHostedCluster("user-ca", "")

	seed, err := newTrustBundleTestConfigGenerator(t, hc, trustBundleTestNodePool(""), true, trustBundleConfigMap("user-ca", "ca-a"))
	g.Expect(err).ToNot(HaveOccurred())
	baseline := seed.TrustBundleContentHash()

	hashFor := func(content string) string {
		cg, err := newTrustBundleTestConfigGenerator(t, hc, trustBundleTestNodePool(baseline), true, trustBundleConfigMap("user-ca", content))
		g.Expect(err).ToNot(HaveOccurred())
		return cg.Hash()
	}

	hashA := hashFor("ca-a")
	hashB := hashFor("ca-b")
	hashC := hashFor("ca-c")
	g.Expect(hashA).To(Equal(seed.Hash()), "content equal to the baseline should keep the seeded hash")
	g.Expect(hashB).ToNot(Equal(hashA), "an edit after seeding should roll out")
	g.Expect(hashC).ToNot(Equal(hashB), "each subsequent edit should roll out")
	g.Expect(hashFor("ca-b")).To(Equal(hashB), "the hash should be deterministic for the same content")
	g.Expect(hashFor("ca-a")).To(Equal(hashA), "reverting to the baseline content should return to the seeded hash")
}

func TestNewConfigGeneratorTrustBundleErrors(t *testing.T) {
	testCases := []struct {
		name           string
		hostedCluster  *hyperv1.HostedCluster
		objects        []crclient.Object
		capable        bool
		expectError    bool
		expectNotFound bool
	}{
		{
			name:           "When the additional trust bundle ConfigMap is missing and the CPO is capable, it should return a not found TrustBundleConfigError",
			hostedCluster:  trustBundleTestHostedCluster("user-ca", ""),
			capable:        true,
			expectError:    true,
			expectNotFound: true,
		},
		{
			name:           "When the proxy trustedCA ConfigMap is missing and the CPO is capable, it should return a not found TrustBundleConfigError",
			hostedCluster:  trustBundleTestHostedCluster("", "proxy-ca"),
			capable:        true,
			expectError:    true,
			expectNotFound: true,
		},
		{
			name:          "When a referenced ConfigMap has no ca-bundle.crt key and the CPO is capable, it should return a TrustBundleConfigError",
			hostedCluster: trustBundleTestHostedCluster("user-ca", ""),
			objects: []crclient.Object{&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: "user-ca", Namespace: trustBundleTestNamespace},
				Data:       map[string]string{"other": "value"},
			}},
			capable:     true,
			expectError: true,
		},
		{
			name:          "When a referenced ConfigMap is missing and the CPO is not capable, it should not read trust bundle ConfigMaps",
			hostedCluster: trustBundleTestHostedCluster("user-ca", "proxy-ca"),
			capable:       false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			_, err := newTrustBundleTestConfigGenerator(t, tc.hostedCluster, trustBundleTestNodePool(""), tc.capable, tc.objects...)
			if !tc.expectError {
				g.Expect(err).ToNot(HaveOccurred())
				return
			}
			g.Expect(err).To(HaveOccurred())
			var trustBundleErr *TrustBundleConfigError
			g.Expect(errors.As(err, &trustBundleErr)).To(BeTrue())
			g.Expect(apierrors.IsNotFound(err)).To(Equal(tc.expectNotFound))
		})
	}
}

func TestRenderedTrustBundleHashDetectsConcurrentEdit(t *testing.T) {
	g := NewWithT(t)
	hc := trustBundleTestHostedCluster("user-ca", "")
	c := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(trustBundleConfigMap("user-ca", "ca-b")).Build()

	_, err := renderedTrustBundleHash(t.Context(), c, hc, supportutil.HashSimple("ca-a"), "")
	g.Expect(err).To(HaveOccurred())
	var trustBundleErr *TrustBundleConfigError
	g.Expect(errors.As(err, &trustBundleErr)).To(BeTrue())
}

func TestSeedTrustBundleBaseline(t *testing.T) {
	testCases := []struct {
		name             string
		existingBaseline string
		contentHash      string
		expectBaseline   string
		expectAnnotation bool
	}{
		{
			name:             "When the CPO is capable and no baseline exists, it should seed the current content",
			contentHash:      "content-a",
			expectBaseline:   "content-a",
			expectAnnotation: true,
		},
		{
			name:             "When a baseline already exists, it should not overwrite it",
			existingBaseline: "content-a",
			contentHash:      "content-b",
			expectBaseline:   "content-a",
			expectAnnotation: true,
		},
		{
			name:             "When the CPO is not capable, it should not seed a baseline",
			contentHash:      "",
			expectAnnotation: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			np := trustBundleTestNodePool(tc.existingBaseline)
			cg := &ConfigGenerator{nodePool: np, trustBundleContentHash: tc.contentHash, rolloutConfig: &rolloutConfig{}}
			seedTrustBundleBaseline(ctrl.Log, np, cg)
			baseline, ok := np.Annotations[nodePoolAnnotationTrustBundleBaseline]
			g.Expect(ok).To(Equal(tc.expectAnnotation))
			g.Expect(baseline).To(Equal(tc.expectBaseline))
		})
	}
}

func TestHostedClusterReferencesConfigMap(t *testing.T) {
	testCases := []struct {
		name          string
		hostedCluster *hyperv1.HostedCluster
		configMapName string
		expected      bool
	}{
		{
			name:          "When ConfigMap is referenced as additionalTrustBundle, it should return true",
			hostedCluster: trustBundleTestHostedCluster("user-ca", ""),
			configMapName: "user-ca",
			expected:      true,
		},
		{
			name:          "When ConfigMap is referenced as proxy trustedCA, it should return true",
			hostedCluster: trustBundleTestHostedCluster("", "proxy-ca"),
			configMapName: "proxy-ca",
			expected:      true,
		},
		{
			name:          "When ConfigMap is not referenced by HostedCluster, it should return false",
			hostedCluster: trustBundleTestHostedCluster("user-ca", "proxy-ca"),
			configMapName: "other-ca",
			expected:      false,
		},
		{
			name:          "When the HostedCluster has no trust bundles and the name is empty, it should return false",
			hostedCluster: trustBundleTestHostedCluster("", ""),
			configMapName: "",
			expected:      false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(hostedClusterReferencesConfigMap(tc.hostedCluster, tc.configMapName)).To(Equal(tc.expected))
		})
	}
}

func TestEnqueueNodePoolsForTrustBundleConfigMap(t *testing.T) {
	hcWithTrustBundles := trustBundleTestHostedCluster("user-ca", "proxy-ca")
	otherHC := trustBundleTestHostedCluster("", "")
	otherHC.Name = "other"
	np1 := &hyperv1.NodePool{ObjectMeta: metav1.ObjectMeta{Name: "np1", Namespace: trustBundleTestNamespace}, Spec: hyperv1.NodePoolSpec{ClusterName: "test"}}
	np2 := &hyperv1.NodePool{ObjectMeta: metav1.ObjectMeta{Name: "np2", Namespace: trustBundleTestNamespace}, Spec: hyperv1.NodePoolSpec{ClusterName: "other"}}
	np3 := &hyperv1.NodePool{ObjectMeta: metav1.ObjectMeta{Name: "np3", Namespace: trustBundleTestNamespace}, Spec: hyperv1.NodePoolSpec{ClusterName: "missing"}}

	testCases := []struct {
		name      string
		configMap string
		getErr    bool
		expected  []string
	}{
		{
			name:      "When the additional trust bundle ConfigMap changes, it should enqueue the HostedCluster NodePools",
			configMap: "user-ca",
			expected:  []string{"np1"},
		},
		{
			name:      "When the proxy trustedCA ConfigMap changes, it should enqueue the HostedCluster NodePools",
			configMap: "proxy-ca",
			expected:  []string{"np1"},
		},
		{
			name:      "When an unreferenced ConfigMap changes, it should not enqueue any NodePool",
			configMap: "unrelated",
			expected:  nil,
		},
		{
			name:      "When getting a HostedCluster fails, it should skip its NodePools",
			configMap: "user-ca",
			getErr:    true,
			expected:  nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			builder := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(hcWithTrustBundles, otherHC, np1, np2, np3)
			if tc.getErr {
				builder = builder.WithInterceptorFuncs(interceptor.Funcs{
					Get: func(ctx context.Context, client crclient.WithWatch, key crclient.ObjectKey, obj crclient.Object, opts ...crclient.GetOption) error {
						if _, ok := obj.(*hyperv1.HostedCluster); ok {
							return errors.New("transient error")
						}
						return client.Get(ctx, key, obj, opts...)
					},
				})
			}
			r := &NodePoolReconciler{Client: builder.Build()}
			requests := r.enqueueNodePoolsForConfig(t.Context(), trustBundleConfigMap(tc.configMap, "ca"))
			var names []string
			for _, req := range requests {
				names = append(names, req.Name)
			}
			g.Expect(names).To(Equal(tc.expected))
		})
	}
}

func TestEnqueueNodePoolsForConfigDeduplicatesTrustBundleReferences(t *testing.T) {
	g := NewWithT(t)
	hc := trustBundleTestHostedCluster("shared-ca", "")
	np := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{Name: "np1", Namespace: trustBundleTestNamespace},
		Spec: hyperv1.NodePoolSpec{
			ClusterName: "test",
			Config:      []corev1.LocalObjectReference{{Name: "shared-ca"}},
		},
	}
	r := &NodePoolReconciler{Client: fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(hc, np).Build()}
	requests := r.enqueueNodePoolsForConfig(t.Context(), trustBundleConfigMap("shared-ca", "ca"))
	g.Expect(requests).To(Equal([]reconcile.Request{{NamespacedName: crclient.ObjectKeyFromObject(np)}}))
}

func TestSetValidMachineConfigConditionForError(t *testing.T) {
	notFound := apierrors.NewNotFound(corev1.Resource("configmaps"), "user-ca")
	testCases := []struct {
		name         string
		err          error
		expectErr    bool
		expectReason string
	}{
		{
			name:         "When a trust bundle ConfigMap is missing, it should soft-fail with reason NotFound",
			err:          &TrustBundleConfigError{err: notFound},
			expectReason: hyperv1.NodePoolNotFoundReason,
		},
		{
			name:         "When a trust bundle ConfigMap is malformed, it should soft-fail with reason ValidationFailed",
			err:          &TrustBundleConfigError{err: errors.New("missing key")},
			expectReason: hyperv1.NodePoolValidationFailedReason,
		},
		{
			name:         "When config generation fails for another reason, it should return an error",
			err:          errors.New("boom"),
			expectErr:    true,
			expectReason: hyperv1.NodePoolValidationFailedReason,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			np := &hyperv1.NodePool{}
			result, err := setValidMachineConfigConditionForError(np, tc.err)
			g.Expect(result).ToNot(BeNil())
			if tc.expectErr {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
			cond := FindStatusCondition(np.Status.Conditions, hyperv1.NodePoolValidMachineConfigConditionType)
			g.Expect(cond).ToNot(BeNil())
			g.Expect(cond.Status).To(Equal(corev1.ConditionFalse))
			g.Expect(cond.Reason).To(Equal(tc.expectReason))
		})
	}
}

func TestReconcileTokenSecretTrustBundleHashes(t *testing.T) {
	testCases := []struct {
		name               string
		renderedHash       string
		expectRenderedKeys bool
	}{
		{
			name:               "When the CPO supports rendered trust bundle validation, it should refresh all trust bundle hashes in place",
			renderedHash:       "rendered-new",
			expectRenderedKeys: true,
		},
		{
			name:               "When the CPO does not support rendered trust bundle validation, it should refresh the additional trust bundle hash and drop rendered keys",
			renderedHash:       "",
			expectRenderedKeys: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			token := &Token{
				ConfigGenerator: &ConfigGenerator{
					hostedCluster:           trustBundleTestHostedCluster("user-ca", "proxy-ca"),
					nodePool:                trustBundleTestNodePool(""),
					renderedTrustBundleHash: tc.renderedHash,
					rolloutConfig:           &rolloutConfig{},
				},
				cpoCapabilities:           &CPOCapabilities{},
				additionalTrustBundleHash: []byte("additional-new"),
				proxyTrustedCAHash:        []byte("proxy-new"),
				globalConfigHash:          []byte("global"),
				cloudConfigHash:           []byte("cloud"),
			}
			secret := &corev1.Secret{
				Data: map[string][]byte{
					TokenSecretTokenKey:                   []byte("token"),
					TokenSecretAdditionalTrustBundleKey:   []byte("additional-old"),
					TokenSecretProxyTrustedCAHashKey:      []byte("proxy-old"),
					TokenSecretRenderedTrustBundleHashKey: []byte("rendered-old"),
				},
			}
			g.Expect(token.reconcileTokenSecret(secret)).To(Succeed())
			g.Expect(string(secret.Data[TokenSecretTokenKey])).To(Equal("token"), "the token must be preserved")
			g.Expect(string(secret.Data[TokenSecretAdditionalTrustBundleKey])).To(Equal("additional-new"))
			if tc.expectRenderedKeys {
				g.Expect(string(secret.Data[TokenSecretProxyTrustedCAHashKey])).To(Equal("proxy-new"))
				g.Expect(string(secret.Data[TokenSecretRenderedTrustBundleHashKey])).To(Equal("rendered-new"))
			} else {
				g.Expect(secret.Data).ToNot(HaveKey(TokenSecretProxyTrustedCAHashKey))
				g.Expect(secret.Data).ToNot(HaveKey(TokenSecretRenderedTrustBundleHashKey))
			}
		})
	}
}
