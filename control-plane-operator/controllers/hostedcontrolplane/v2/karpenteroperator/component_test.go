package karpenteroperator

import (
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestPredicate(t *testing.T) {
	t.Parallel()

	awsAutoNode := hyperv1.AutoNode{
		Provisioner: hyperv1.ProvisionerConfig{
			Name: hyperv1.ProvisionerKarpenter,
			Karpenter: hyperv1.KarpenterConfig{
				Platform: hyperv1.AWSPlatform,
				AWS: hyperv1.KarpenterAWSConfig{
					RoleARN: "arn:aws:iam::123456789012:role/karpenter",
				},
			},
		},
	}
	azureAutoNode := hyperv1.AutoNode{
		Provisioner: hyperv1.ProvisionerConfig{
			Name: hyperv1.ProvisionerKarpenter,
			Karpenter: hyperv1.KarpenterConfig{
				Platform: hyperv1.AzurePlatform,
				Azure: hyperv1.KarpenterAzureConfig{
					ClientID: "12345678-1234-1234-1234-123456789012",
				},
			},
		},
	}
	autoNodeByPlatform := map[hyperv1.PlatformType]hyperv1.AutoNode{
		hyperv1.AWSPlatform:   awsAutoNode,
		hyperv1.AzurePlatform: azureAutoNode,
	}

	platformMatrix := []struct {
		name                               string
		kubeconfigSecret                   *corev1.Secret
		kubeconfigSecretRef                *hyperv1.KubeconfigSecretRef
		standaloneKarpenterOperatorEnabled bool
		expectedByPlatform                 map[hyperv1.PlatformType]bool
		expectError                        bool
	}{
		{
			name:                "When Karpenter is enabled, it should return based on the platform",
			kubeconfigSecretRef: &hyperv1.KubeconfigSecretRef{Name: "hcco-kubeconfig"},
			kubeconfigSecret:    &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "hcco-kubeconfig", Namespace: "test-namespace"}},
			expectedByPlatform: map[hyperv1.PlatformType]bool{
				hyperv1.AWSPlatform:   true,
				hyperv1.AzurePlatform: false,
			},
		},
		{
			name:                               "When Karpenter is enabled with standalone operator, it should return true",
			kubeconfigSecretRef:                &hyperv1.KubeconfigSecretRef{Name: "hcco-kubeconfig"},
			kubeconfigSecret:                   &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "hcco-kubeconfig", Namespace: "test-namespace"}},
			standaloneKarpenterOperatorEnabled: true,
			expectedByPlatform: map[hyperv1.PlatformType]bool{
				hyperv1.AWSPlatform:   true,
				hyperv1.AzurePlatform: true,
			},
		},
		{
			name: "When kubeconfig status is nil, it should return false",
			expectedByPlatform: map[hyperv1.PlatformType]bool{
				hyperv1.AWSPlatform:   false,
				hyperv1.AzurePlatform: false,
			},
		},
		{
			name:                               "When kubeconfig secret does not exist, it should return an error",
			kubeconfigSecretRef:                &hyperv1.KubeconfigSecretRef{Name: "hcco-kubeconfig"},
			standaloneKarpenterOperatorEnabled: true,
			expectedByPlatform: map[hyperv1.PlatformType]bool{
				hyperv1.AWSPlatform:   false,
				hyperv1.AzurePlatform: false,
			},
			expectError: true,
		},
	}

	for _, platform := range []hyperv1.PlatformType{hyperv1.AWSPlatform, hyperv1.AzurePlatform} {
		for _, tc := range platformMatrix {
			t.Run(fmt.Sprintf("%s/%s", platform, tc.name), func(t *testing.T) {
				t.Parallel()
				g := NewWithT(t)

				hcp := &hyperv1.HostedControlPlane{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-hcp",
						Namespace: "test-namespace",
					},
					Spec: hyperv1.HostedControlPlaneSpec{
						Platform: hyperv1.PlatformSpec{Type: platform},
						AutoNode: autoNodeByPlatform[platform],
					},
					Status: hyperv1.HostedControlPlaneStatus{
						KubeConfig: tc.kubeconfigSecretRef,
					},
				}

				clientBuilder := fake.NewClientBuilder().WithScheme(api.Scheme)
				if tc.kubeconfigSecret != nil {
					kubeconfigSecret := *tc.kubeconfigSecret
					clientBuilder = clientBuilder.WithObjects(&kubeconfigSecret)
				}

				result, err := predicate(controlplanecomponent.WorkloadContext{
					Context: t.Context(),
					Client:  clientBuilder.Build(),
					HCP:     hcp,
				}, &KarpenterOperatorOptions{
					StandaloneKarpenterOperatorEnabled: tc.standaloneKarpenterOperatorEnabled,
				})

				if tc.expectError {
					g.Expect(err).To(HaveOccurred())
				} else {
					g.Expect(err).ToNot(HaveOccurred())
				}
				g.Expect(result).To(Equal(tc.expectedByPlatform[platform]))
			})
		}
	}

	for _, tc := range []struct {
		name     string
		autoNode hyperv1.AutoNode
	}{
		{
			name: "When Karpenter is not enabled, return false",
			autoNode: hyperv1.AutoNode{
				Provisioner: hyperv1.ProvisionerConfig{Name: ""},
			},
		},
		{
			name:     "When autoNode is empty, return false",
			autoNode: hyperv1.AutoNode{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			result, err := predicate(controlplanecomponent.WorkloadContext{
				Context: t.Context(),
				Client:  fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
				HCP: &hyperv1.HostedControlPlane{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-hcp",
						Namespace: "test-namespace",
					},
					Spec: hyperv1.HostedControlPlaneSpec{
						AutoNode: tc.autoNode,
					},
				},
			}, &KarpenterOperatorOptions{})

			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(BeFalse())
		})
	}
}

func TestKarpenterOperatorOptions_IsRequestServing(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	opts := &KarpenterOperatorOptions{}
	g.Expect(opts.IsRequestServing()).To(BeFalse())
}

func TestKarpenterOperatorOptions_MultiZoneSpread(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	opts := &KarpenterOperatorOptions{}
	g.Expect(opts.MultiZoneSpread()).To(BeFalse())
}

func TestKarpenterOperatorOptions_NeedsManagementKASAccess(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	opts := &KarpenterOperatorOptions{}
	g.Expect(opts.NeedsManagementKASAccess()).To(BeTrue())
}
