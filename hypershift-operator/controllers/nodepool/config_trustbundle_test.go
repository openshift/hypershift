package nodepool

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	api "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/certs"
	supportutil "github.com/openshift/hypershift/support/util"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

func TestHostedClusterReferencesConfigMap(t *testing.T) {
	testCases := []struct {
		name          string
		hostedCluster *hyperv1.HostedCluster
		configMapName string
		expected      bool
	}{
		{
			name: "When ConfigMap is referenced as additionalTrustBundle, it should return true",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					AdditionalTrustBundle: &corev1.LocalObjectReference{Name: "user-ca"},
				},
			},
			configMapName: "user-ca",
			expected:      true,
		},
		{
			name: "When ConfigMap is referenced as proxy trustedCA, it should return true",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Configuration: &hyperv1.ClusterConfiguration{
						Proxy: &configv1.ProxySpec{
							TrustedCA: configv1.ConfigMapNameReference{Name: "proxy-ca"},
						},
					},
				},
			},
			configMapName: "proxy-ca",
			expected:      true,
		},
		{
			name: "When ConfigMap is not referenced by HostedCluster, it should return false",
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					AdditionalTrustBundle: &corev1.LocalObjectReference{Name: "user-ca"},
				},
			},
			configMapName: "other-ca",
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

func TestRolloutTrustBundleHashes(t *testing.T) {
	testCases := []struct {
		name                          string
		hostedCluster                 *hyperv1.HostedCluster
		objects                       []crclient.Object
		expectedAdditionalTrustBundle string
		expectedProxyTrustedCA        string
		expectError                   bool
	}{
		{
			name: "When additionalTrustBundle content changes, it should produce a different hash",
			hostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "test"},
				Spec: hyperv1.HostedClusterSpec{
					AdditionalTrustBundle: &corev1.LocalObjectReference{Name: "user-ca"},
				},
			},
			objects: []crclient.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "user-ca"},
					Data:       map[string]string{certs.UserCABundleMapKey: "bundle-a"},
				},
			},
			expectedAdditionalTrustBundle: supportutil.HashSimple("bundle-a"),
		},
		{
			name: "When proxy trustedCA is set, it should hash the ConfigMap content",
			hostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "test"},
				Spec: hyperv1.HostedClusterSpec{
					Configuration: &hyperv1.ClusterConfiguration{
						Proxy: &configv1.ProxySpec{
							TrustedCA: configv1.ConfigMapNameReference{Name: "proxy-ca"},
						},
					},
				},
			},
			objects: []crclient.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "proxy-ca"},
					Data:       map[string]string{certs.UserCABundleMapKey: "proxy-bundle"},
				},
			},
			expectedProxyTrustedCA: supportutil.HashSimple("proxy-bundle"),
		},
		{
			name: "When referenced ConfigMap is missing ca-bundle.crt, it should return an error",
			hostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "test"},
				Spec: hyperv1.HostedClusterSpec{
					AdditionalTrustBundle: &corev1.LocalObjectReference{Name: "user-ca"},
				},
			},
			objects: []crclient.Object{
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "user-ca"},
					Data:       map[string]string{"other-key": "value"},
				},
			},
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			client := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(tc.objects...).Build()
			additionalHash, proxyHash, err := rolloutTrustBundleHashes(t.Context(), client, tc.hostedCluster)
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(additionalHash).To(Equal(tc.expectedAdditionalTrustBundle))
			g.Expect(proxyHash).To(Equal(tc.expectedProxyTrustedCA))
		})
	}
}

func TestEnqueueNodePoolsForHostedClusterReferencedConfig(t *testing.T) {
	g := NewWithT(t)

	hostedCluster := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "test-hc"},
		Spec: hyperv1.HostedClusterSpec{
			Configuration: &hyperv1.ClusterConfiguration{
				Proxy: &configv1.ProxySpec{
					TrustedCA: configv1.ConfigMapNameReference{Name: "proxy-ca"},
				},
			},
		},
	}
	nodePool := &hyperv1.NodePool{
		ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "workers"},
		Spec: hyperv1.NodePoolSpec{
			ClusterName: "test-hc",
		},
	}
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "proxy-ca"},
		Data:       map[string]string{certs.UserCABundleMapKey: "updated-bundle"},
	}

	reconciler := &NodePoolReconciler{
		Client: fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(hostedCluster, nodePool).Build(),
	}

	requests := reconciler.enqueueNodePoolsForConfig(t.Context(), configMap)
	g.Expect(requests).To(ConsistOf(reconcile.Request{NamespacedName: crclient.ObjectKeyFromObject(nodePool)}))
}
