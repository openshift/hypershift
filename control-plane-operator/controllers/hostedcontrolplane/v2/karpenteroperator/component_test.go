package karpenteroperator

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestPredicate(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name             string
		autoNode         hyperv1.AutoNode
		hcpStatus        *hyperv1.KubeconfigSecretRef
		kubeconfigSecret client.Object
		expected         bool
		expectError      bool
	}{
		{
			name: "When Karpenter is enabled and kubeconfig exists, it should return true",
			autoNode: hyperv1.AutoNode{
				Provisioner: hyperv1.ProvisionerConfig{
					Name: hyperv1.ProvisionerKarpenter,
					Karpenter: hyperv1.KarpenterConfig{
						Platform: hyperv1.AWSPlatform,
						AWS: hyperv1.KarpenterAWSConfig{
							RoleARN: "arn:aws:iam::123456789012:role/karpenter",
						},
					},
				},
			},
			hcpStatus: &hyperv1.KubeconfigSecretRef{
				Name: "hcco-kubeconfig",
			},
			kubeconfigSecret: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "hcco-kubeconfig",
					Namespace: "test-namespace",
				},
			},
			expected:    true,
			expectError: false,
		},
		{
			name: "When Karpenter is not enabled, it should return false",
			autoNode: hyperv1.AutoNode{
				Provisioner: hyperv1.ProvisionerConfig{
					Name: "",
				},
			},
			expected:    false,
			expectError: false,
		},
		{
			name:     "When autoNode is nil, it should return false",
			autoNode: hyperv1.AutoNode{},
			expected: false,
		},
		{
			name: "When kubeconfig status is nil, it should return false",
			autoNode: hyperv1.AutoNode{
				Provisioner: hyperv1.ProvisionerConfig{
					Name: hyperv1.ProvisionerKarpenter,
					Karpenter: hyperv1.KarpenterConfig{
						Platform: hyperv1.AWSPlatform,
						AWS: hyperv1.KarpenterAWSConfig{
							RoleARN: "arn:aws:iam::123456789012:role/karpenter",
						},
					},
				},
			},
			hcpStatus:   nil,
			expected:    false,
			expectError: false,
		},
		{
			name: "When kubeconfig secret does not exist, it should return error",
			autoNode: hyperv1.AutoNode{
				Provisioner: hyperv1.ProvisionerConfig{
					Name: hyperv1.ProvisionerKarpenter,
					Karpenter: hyperv1.KarpenterConfig{
						Platform: hyperv1.AWSPlatform,
						AWS: hyperv1.KarpenterAWSConfig{
							RoleARN: "arn:aws:iam::123456789012:role/karpenter",
						},
					},
				},
			},
			hcpStatus: &hyperv1.KubeconfigSecretRef{
				Name: "hcco-kubeconfig",
			},
			kubeconfigSecret: nil,
			expected:         false,
			expectError:      true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					AutoNode: tc.autoNode,
				},
				Status: hyperv1.HostedControlPlaneStatus{
					KubeConfig: tc.hcpStatus,
				},
			}

			clientBuilder := fake.NewClientBuilder().WithScheme(api.Scheme)
			if tc.kubeconfigSecret != nil {
				clientBuilder = clientBuilder.WithObjects(tc.kubeconfigSecret)
			}
			client := clientBuilder.Build()

			cpContext := controlplanecomponent.WorkloadContext{
				Context: t.Context(),
				Client:  client,
				HCP:     hcp,
			}

			result, err := predicate(cpContext)

			if tc.expectError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
			g.Expect(result).To(Equal(tc.expected))
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
