package karpenteroperator

import (
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/hostedclusterconfigoperator/api"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/upsert"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client"
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

func TestKarpenterCredentialsSecretReconcile(t *testing.T) {
	t.Parallel()

	const (
		namespace = "test-namespace"
		roleARN   = "arn:aws:iam::123456789012:role/karpenter"
		clientID  = "12345678-1234-1234-1234-123456789012"
	)

	hcpWithKarpenterPlatform := func(platform hyperv1.PlatformType) *hyperv1.HostedControlPlane {
		karpenter := hyperv1.KarpenterConfig{Platform: platform}
		if platform == hyperv1.AWSPlatform {
			karpenter.AWS = hyperv1.KarpenterAWSConfig{RoleARN: roleARN}
		}
		if platform == hyperv1.AzurePlatform {
			karpenter.Azure = hyperv1.KarpenterAzureConfig{ClientID: clientID}
		}
		spec := hyperv1.HostedControlPlaneSpec{
			Platform:     hyperv1.PlatformSpec{Type: platform},
			ReleaseImage: "quay.io/openshift-release-dev/ocp-release:4.16.10-x86_64",
			AutoNode: hyperv1.AutoNode{
				Provisioner: hyperv1.ProvisionerConfig{
					Name:      hyperv1.ProvisionerKarpenter,
					Karpenter: karpenter,
				},
			},
		}
		if platform == hyperv1.AWSPlatform {
			spec.Platform.AWS = &hyperv1.AWSPlatformSpec{Region: "us-east-1"}
		}
		if platform == hyperv1.AzurePlatform {
			spec.Platform.Azure = &hyperv1.AzurePlatformSpec{
				Location:       "eastus",
				TenantID:       "tenant-id",
				SubscriptionID: "subscription-id",
			}
		}
		return &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-hcp",
				Namespace: namespace,
				UID:       "test-uid",
			},
			Spec: spec,
		}
	}

	componentOpts := &KarpenterOperatorOptions{
		HyperShiftOperatorImage:   "test-image",
		ControlPlaneOperatorImage: "cpo-image",
		IgnitionEndpoint:          "https://ignition.example.com",
	}

	testCases := []struct {
		name              string
		platform          hyperv1.PlatformType
		expectSecretExist bool
	}{
		{
			name:              "When platform is AWS it should create the credentials secret",
			platform:          hyperv1.AWSPlatform,
			expectSecretExist: true,
		},
		{
			name:              "When platform is Azure it should not create the credentials secret",
			platform:          hyperv1.AzurePlatform,
			expectSecretExist: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			cpContext := controlplanecomponent.ControlPlaneContext{
				Context:                t.Context(),
				HCP:                    hcpWithKarpenterPlatform(tc.platform),
				Client:                 fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
				ApplyProvider:          upsert.NewApplyProvider(false),
				ReleaseImageProvider:   testutil.FakeImageProvider(),
				SkipPredicate:          true,
				SkipCertificateSigning: true,
				OmitOwnerReference:     true,
			}

			g.Expect(NewComponent(componentOpts).Reconcile(cpContext)).To(Succeed())

			got := &corev1.Secret{}
			err := cpContext.Client.Get(t.Context(), client.ObjectKey{
				Namespace: namespace,
				Name:      "karpenter-credentials",
			}, got)
			if tc.expectSecretExist {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(string(got.Data["credentials"])).To(ContainSubstring("role_arn = " + roleARN))
			} else {
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
			}
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
