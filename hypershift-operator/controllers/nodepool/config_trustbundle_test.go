package nodepool

import (
	"errors"
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
		expectTrustBundleError        bool
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
			name: "When referenced ConfigMap is missing ca-bundle.crt, it should return a TrustBundleConfigError",
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
			expectError:            true,
			expectTrustBundleError: true,
		},
		{
			name: "When referenced ConfigMap does not exist, it should return a TrustBundleConfigError",
			hostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "test"},
				Spec: hyperv1.HostedClusterSpec{
					AdditionalTrustBundle: &corev1.LocalObjectReference{Name: "user-ca"},
				},
			},
			expectError:            true,
			expectTrustBundleError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			client := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(tc.objects...).Build()
			additionalHash, proxyHash, err := rolloutTrustBundleHashes(t.Context(), client, tc.hostedCluster)
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
				if tc.expectTrustBundleError {
					var trustBundleErr *TrustBundleConfigError
					g.Expect(errors.As(err, &trustBundleErr)).To(BeTrue())
				}
				return
			}
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(additionalHash).To(Equal(tc.expectedAdditionalTrustBundle))
			g.Expect(proxyHash).To(Equal(tc.expectedProxyTrustedCA))
		})
	}
}

func TestEnqueueNodePoolsForHostedClusterReferencedConfig(t *testing.T) {
	hostedClusterWithBoth := &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "test-hc"},
		Spec: hyperv1.HostedClusterSpec{
			AdditionalTrustBundle: &corev1.LocalObjectReference{Name: "user-ca"},
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

	testCases := []struct {
		name          string
		hostedCluster *hyperv1.HostedCluster
		configMapName string
		expectEnqueue bool
	}{
		{
			name: "When ConfigMap is referenced as proxy.trustedCA, it should enqueue the NodePool",
			hostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "test-hc"},
				Spec: hyperv1.HostedClusterSpec{
					Configuration: &hyperv1.ClusterConfiguration{
						Proxy: &configv1.ProxySpec{
							TrustedCA: configv1.ConfigMapNameReference{Name: "proxy-ca"},
						},
					},
				},
			},
			configMapName: "proxy-ca",
			expectEnqueue: true,
		},
		{
			name: "When ConfigMap is referenced as additionalTrustBundle, it should enqueue the NodePool",
			hostedCluster: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: "test-hc"},
				Spec: hyperv1.HostedClusterSpec{
					AdditionalTrustBundle: &corev1.LocalObjectReference{Name: "user-ca"},
				},
			},
			configMapName: "user-ca",
			expectEnqueue: true,
		},
		{
			name:          "When ConfigMap is not referenced by HostedCluster, it should not enqueue the NodePool",
			hostedCluster: hostedClusterWithBoth,
			configMapName: "unrelated-ca",
			expectEnqueue: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			configMap := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Namespace: "clusters", Name: tc.configMapName},
				Data:       map[string]string{certs.UserCABundleMapKey: "updated-bundle"},
			}

			reconciler := &NodePoolReconciler{
				Client: fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(tc.hostedCluster, nodePool).Build(),
			}

			requests := reconciler.enqueueNodePoolsForConfig(t.Context(), configMap)
			if tc.expectEnqueue {
				g.Expect(requests).To(ConsistOf(reconcile.Request{NamespacedName: crclient.ObjectKeyFromObject(nodePool)}))
			} else {
				g.Expect(requests).To(BeEmpty())
			}
		})
	}
}
