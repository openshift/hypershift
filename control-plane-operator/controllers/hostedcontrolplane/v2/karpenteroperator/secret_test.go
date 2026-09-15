package karpenteroperator

import (
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/config"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAdaptCredentialsSecret(t *testing.T) {
	t.Parallel()

	const clientID = "12345678-1234-1234-1234-123456789012"

	testCases := []struct {
		name        string
		hcp         *hyperv1.HostedControlPlane
		expectError string
		validate    func(t *testing.T, g Gomega, secret *corev1.Secret)
	}{
		{
			name: "When AWS role ARN is provided, it should generate correct credentials format",
			hcp: hcpWithKarpenter(hyperv1.KarpenterConfig{
				Platform: hyperv1.AWSPlatform,
				AWS:      hyperv1.KarpenterAWSConfig{RoleARN: "arn:aws:iam::123456789012:role/karpenter-role"},
			}),
			validate: func(t *testing.T, g Gomega, secret *corev1.Secret) {
				t.Helper()
				credentials := string(secret.Data["credentials"])
				g.Expect(credentials).To(ContainSubstring("[default]"))
				g.Expect(credentials).To(ContainSubstring("role_arn = arn:aws:iam::123456789012:role/karpenter-role"))
				g.Expect(credentials).To(ContainSubstring("web_identity_token_file = /var/run/secrets/openshift/serviceaccount/token"))
				g.Expect(credentials).To(ContainSubstring("sts_regional_endpoints = regional"))
			},
		},
		{
			name: "When different AWS role ARN format is provided, it should be included correctly",
			hcp: hcpWithKarpenter(hyperv1.KarpenterConfig{
				Platform: hyperv1.AWSPlatform,
				AWS:      hyperv1.KarpenterAWSConfig{RoleARN: "arn:aws:iam::999999999999:role/my-custom-karpenter-role"},
			}),
			validate: func(t *testing.T, g Gomega, secret *corev1.Secret) {
				t.Helper()
				g.Expect(string(secret.Data["credentials"])).To(ContainSubstring("role_arn = arn:aws:iam::999999999999:role/my-custom-karpenter-role"))
			},
		},
		{
			name: "When AWS role ARN has path component, it should be preserved",
			hcp: hcpWithKarpenter(hyperv1.KarpenterConfig{
				Platform: hyperv1.AWSPlatform,
				AWS:      hyperv1.KarpenterAWSConfig{RoleARN: "arn:aws:iam::111111111111:role/path/to/role/karpenter"},
			}),
			validate: func(t *testing.T, g Gomega, secret *corev1.Secret) {
				t.Helper()
				g.Expect(string(secret.Data["credentials"])).To(ContainSubstring("role_arn = arn:aws:iam::111111111111:role/path/to/role/karpenter"))
			},
		},
		{
			name: "When AWS credentials are generated, it should match the expected template",
			hcp: hcpWithKarpenter(hyperv1.KarpenterConfig{
				Platform: hyperv1.AWSPlatform,
				AWS:      hyperv1.KarpenterAWSConfig{RoleARN: "arn:aws:iam::123456789012:role/test-role"},
			}),
			validate: func(t *testing.T, g Gomega, secret *corev1.Secret) {
				t.Helper()
				roleARN := "arn:aws:iam::123456789012:role/test-role"
				expectedTemplate := "[default]\n\t\trole_arn = %s\n\t\tweb_identity_token_file = /var/run/secrets/openshift/serviceaccount/token\n\t\tsts_regional_endpoints = regional\n\t"
				g.Expect(string(secret.Data["credentials"])).To(Equal(fmt.Sprintf(expectedTemplate, roleARN)))
			},
		},
		{
			name: "When Azure platform and client ID are provided, it should populate Azure credential keys",
			hcp: hcpWithKarpenter(hyperv1.KarpenterConfig{
				Platform: hyperv1.AzurePlatform,
				Azure:    hyperv1.KarpenterAzureConfig{ClientID: hyperv1.AzureClientID(clientID)},
			}, hyperv1.PlatformSpec{
				Type: hyperv1.AzurePlatform,
				Azure: &hyperv1.AzurePlatformSpec{
					TenantID:       "tenant-id",
					SubscriptionID: "subscription-id",
				},
			}),
			validate: func(t *testing.T, g Gomega, secret *corev1.Secret) {
				t.Helper()
				g.Expect(secret.Data).To(HaveKeyWithValue("azure_client_id", []byte(clientID)))
				g.Expect(secret.Data).To(HaveKeyWithValue("azure_tenant_id", []byte("tenant-id")))
				g.Expect(secret.Data).To(HaveKeyWithValue("azure_subscription_id", []byte("subscription-id")))
				g.Expect(secret.Data).To(HaveKeyWithValue("azure_federated_token_file", []byte(config.CloudTokenMountPath+"/token")))
				g.Expect(secret.Data).ToNot(HaveKey("credentials"))
			},
		},
		{
			name: "When Azure client ID is missing, it should return an error",
			hcp: hcpWithKarpenter(hyperv1.KarpenterConfig{
				Platform: hyperv1.AzurePlatform,
			}, hyperv1.PlatformSpec{
				Type: hyperv1.AzurePlatform,
				Azure: &hyperv1.AzurePlatformSpec{
					TenantID:       "tenant-id",
					SubscriptionID: "subscription-id",
				},
			}),
			expectError: "AutoNode Karpenter Azure clientID is required",
		},
		{
			name: "When platform is unsupported, it should return an error",
			hcp: hcpWithKarpenter(hyperv1.KarpenterConfig{
				Platform: hyperv1.GCPPlatform,
			}),
			expectError: "unsupported platform: GCP",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "karpenter-credentials",
					Namespace: "test-namespace",
				},
			}

			err := adaptCredentialsSecret(controlplanecomponent.WorkloadContext{
				Context: t.Context(),
				HCP:     tc.hcp,
			}, secret)

			if tc.expectError != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(Equal(tc.expectError))
				return
			}

			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(secret.Type).To(Equal(corev1.SecretTypeOpaque))
			if tc.validate != nil {
				tc.validate(t, g, secret)
			}
		})
	}
}

func hcpWithKarpenter(karpenter hyperv1.KarpenterConfig, platform ...hyperv1.PlatformSpec) *hyperv1.HostedControlPlane {
	spec := hyperv1.HostedControlPlaneSpec{
		AutoNode: hyperv1.AutoNode{
			Provisioner: hyperv1.ProvisionerConfig{
				Karpenter: karpenter,
			},
		},
	}
	if len(platform) > 0 {
		spec.Platform = platform[0]
	}

	return &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "test-namespace",
		},
		Spec: spec,
	}
}
