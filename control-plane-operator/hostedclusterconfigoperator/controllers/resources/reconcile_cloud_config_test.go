package resources

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/aws"
	cpomanifests "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/certs"

	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestReconcileCloudConfig_AWS(t *testing.T) {
	const (
		hcpNamespace = "test-hcp-ns"
	)

	fakeAWSCloudConfig := func() *corev1.ConfigMap {
		cm := cpomanifests.AWSProviderConfig(hcpNamespace)
		cm.Data = map[string]string{
			aws.ProviderConfigKey: "[Global]\nZone = us-east-1a\nVPC = vpc-123\n",
		}
		return cm
	}

	fakeManagedTrustBundle := func(caData string) *corev1.ConfigMap {
		cm := cpomanifests.TrustedCABundleConfigMap(hcpNamespace)
		cm.Data = map[string]string{
			certs.UserCABundleMapKey: caData,
		}
		return cm
	}

	testCases := []struct {
		name         string
		hcp          *hyperv1.HostedControlPlane
		cpObjects    []client.Object
		guestObjects []client.Object
		expectError  bool
		errContains  string
		verify       func(g Gomega, guestClient client.Client)
	}{
		{
			name: "When AWS platform with additionalTrustBundle, it should create cloud-provider-config with CA bundle",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: hcpNamespace,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
					},
					AdditionalTrustBundle: &corev1.LocalObjectReference{
						Name: "user-ca-bundle",
					},
				},
			},
			cpObjects: []client.Object{
				fakeAWSCloudConfig(),
				fakeManagedTrustBundle("-----BEGIN CERTIFICATE-----\nfake-ca-bundle\n-----END CERTIFICATE-----\n"),
			},
			verify: func(g Gomega, guestClient client.Client) {
				cm := &corev1.ConfigMap{}
				err := guestClient.Get(context.Background(), client.ObjectKey{
					Namespace: ConfigNamespace,
					Name:      CloudProviderCMName,
				}, cm)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cm.Data).To(HaveKeyWithValue(aws.ProviderConfigKey, "[Global]\nZone = us-east-1a\nVPC = vpc-123\n"))
				g.Expect(cm.Data).To(HaveKeyWithValue(aws.CABundleKey, "-----BEGIN CERTIFICATE-----\nfake-ca-bundle\n-----END CERTIFICATE-----\n"))
			},
		},
		{
			name: "When AWS platform with proxy TrustedCA, it should create cloud-provider-config with CA bundle",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: hcpNamespace,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
					},
					Configuration: &hyperv1.ClusterConfiguration{
						Proxy: &configv1.ProxySpec{
							TrustedCA: configv1.ConfigMapNameReference{
								Name: "proxy-trusted-ca",
							},
						},
					},
				},
			},
			cpObjects: []client.Object{
				fakeAWSCloudConfig(),
				fakeManagedTrustBundle("-----BEGIN CERTIFICATE-----\nproxy-ca-bundle\n-----END CERTIFICATE-----\n"),
			},
			verify: func(g Gomega, guestClient client.Client) {
				cm := &corev1.ConfigMap{}
				err := guestClient.Get(context.Background(), client.ObjectKey{
					Namespace: ConfigNamespace,
					Name:      CloudProviderCMName,
				}, cm)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cm.Data).To(HaveKeyWithValue(aws.ProviderConfigKey, "[Global]\nZone = us-east-1a\nVPC = vpc-123\n"))
				g.Expect(cm.Data).To(HaveKeyWithValue(aws.CABundleKey, "-----BEGIN CERTIFICATE-----\nproxy-ca-bundle\n-----END CERTIFICATE-----\n"))
			},
		},
		{
			name: "When AWS platform without any trust bundle, it should create cloud-provider-config without CA bundle",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: hcpNamespace,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
					},
				},
			},
			cpObjects: []client.Object{
				fakeAWSCloudConfig(),
			},
			verify: func(g Gomega, guestClient client.Client) {
				cm := &corev1.ConfigMap{}
				err := guestClient.Get(context.Background(), client.ObjectKey{
					Namespace: ConfigNamespace,
					Name:      CloudProviderCMName,
				}, cm)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cm.Data).To(HaveKeyWithValue(aws.ProviderConfigKey, "[Global]\nZone = us-east-1a\nVPC = vpc-123\n"))
				g.Expect(cm.Data).ToNot(HaveKey(aws.CABundleKey))
			},
		},
		{
			name: "When trust bundle is removed, it should remove CA bundle key from cloud-provider-config",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: hcpNamespace,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
					},
				},
			},
			cpObjects: []client.Object{
				fakeAWSCloudConfig(),
			},
			guestObjects: []client.Object{
				// Simulate a previously existing cloud-provider-config with CA bundle
				&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Namespace: ConfigNamespace,
						Name:      CloudProviderCMName,
					},
					Data: map[string]string{
						aws.ProviderConfigKey: "[Global]\nZone = us-east-1a\nVPC = vpc-123\n",
						aws.CABundleKey:       "old-ca-bundle",
					},
				},
			},
			verify: func(g Gomega, guestClient client.Client) {
				cm := &corev1.ConfigMap{}
				err := guestClient.Get(context.Background(), client.ObjectKey{
					Namespace: ConfigNamespace,
					Name:      CloudProviderCMName,
				}, cm)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cm.Data).To(HaveKeyWithValue(aws.ProviderConfigKey, "[Global]\nZone = us-east-1a\nVPC = vpc-123\n"))
				g.Expect(cm.Data).ToNot(HaveKey(aws.CABundleKey))
			},
		},
		{
			name: "When aws-cloud-config ConfigMap is missing, it should return an error",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: hcpNamespace,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
					},
				},
			},
			cpObjects:   []client.Object{},
			expectError: true,
			errContains: "not found",
		},
		{
			name: "When additionalTrustBundle is set but managed trust bundle ConfigMap is missing, it should sync base config without CA bundle",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: hcpNamespace,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
					},
					AdditionalTrustBundle: &corev1.LocalObjectReference{
						Name: "user-ca-bundle",
					},
				},
			},
			cpObjects: []client.Object{
				fakeAWSCloudConfig(),
			},
			verify: func(g Gomega, guestClient client.Client) {
				cm := &corev1.ConfigMap{}
				err := guestClient.Get(context.Background(), client.ObjectKey{
					Namespace: ConfigNamespace,
					Name:      CloudProviderCMName,
				}, cm)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cm.Data).To(HaveKeyWithValue(aws.ProviderConfigKey, "[Global]\nZone = us-east-1a\nVPC = vpc-123\n"))
				g.Expect(cm.Data).ToNot(HaveKey(aws.CABundleKey))
			},
		},
		{
			name: "When aws-cloud-config ConfigMap is missing the provider config key, it should return an error",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: hcpNamespace,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
					},
				},
			},
			cpObjects: []client.Object{
				func() *corev1.ConfigMap {
					cm := cpomanifests.AWSProviderConfig(hcpNamespace)
					cm.Data = map[string]string{}
					return cm
				}(),
			},
			expectError: true,
			errContains: aws.ProviderConfigKey,
		},
		{
			name: "When aws-cloud-config ConfigMap has whitespace-only provider config, it should return an error",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: hcpNamespace,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
					},
				},
			},
			cpObjects: []client.Object{
				func() *corev1.ConfigMap {
					cm := cpomanifests.AWSProviderConfig(hcpNamespace)
					cm.Data = map[string]string{
						aws.ProviderConfigKey: "   \n\t",
					}
					return cm
				}(),
			},
			expectError: true,
			errContains: aws.ProviderConfigKey,
		},
		{
			name: "When proxy TrustedCA is set but managed trust bundle ConfigMap is missing, it should sync base config without CA bundle",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: hcpNamespace,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
					},
					Configuration: &hyperv1.ClusterConfiguration{
						Proxy: &configv1.ProxySpec{
							TrustedCA: configv1.ConfigMapNameReference{
								Name: "proxy-trusted-ca",
							},
						},
					},
				},
			},
			cpObjects: []client.Object{
				fakeAWSCloudConfig(),
			},
			verify: func(g Gomega, guestClient client.Client) {
				cm := &corev1.ConfigMap{}
				err := guestClient.Get(context.Background(), client.ObjectKey{
					Namespace: ConfigNamespace,
					Name:      CloudProviderCMName,
				}, cm)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cm.Data).To(HaveKeyWithValue(aws.ProviderConfigKey, "[Global]\nZone = us-east-1a\nVPC = vpc-123\n"))
				g.Expect(cm.Data).ToNot(HaveKey(aws.CABundleKey))
			},
		},
		{
			name: "When managed trust bundle has empty ca-bundle.crt value, it should not set CA bundle key",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test",
					Namespace: hcpNamespace,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AWSPlatform,
					},
					AdditionalTrustBundle: &corev1.LocalObjectReference{
						Name: "user-ca-bundle",
					},
				},
			},
			cpObjects: []client.Object{
				fakeAWSCloudConfig(),
				fakeManagedTrustBundle(""),
			},
			verify: func(g Gomega, guestClient client.Client) {
				cm := &corev1.ConfigMap{}
				err := guestClient.Get(context.Background(), client.ObjectKey{
					Namespace: ConfigNamespace,
					Name:      CloudProviderCMName,
				}, cm)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(cm.Data).To(HaveKeyWithValue(aws.ProviderConfigKey, "[Global]\nZone = us-east-1a\nVPC = vpc-123\n"))
				g.Expect(cm.Data).ToNot(HaveKey(aws.CABundleKey))
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			cpClient := fake.NewClientBuilder().WithObjects(tc.cpObjects...).Build()

			guestObjects := tc.guestObjects
			guestClient := fake.NewClientBuilder().WithObjects(guestObjects...).Build()

			r := &reconciler{
				client:                 guestClient,
				cpClient:               cpClient,
				CreateOrUpdateProvider: &simpleCreateOrUpdater{},
			}

			err := r.reconcileCloudConfig(context.Background(), tc.hcp)
			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
				if tc.errContains != "" {
					g.Expect(err.Error()).To(ContainSubstring(tc.errContains))
				}
				return
			}
			g.Expect(err).ToNot(HaveOccurred())

			tc.verify(g, guestClient)
		})
	}
}
