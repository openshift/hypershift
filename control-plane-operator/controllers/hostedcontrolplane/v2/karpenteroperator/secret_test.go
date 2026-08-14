package karpenteroperator

import (
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	controlplanecomponent "github.com/openshift/hypershift/support/controlplane-component"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAdaptCredentialsSecret(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name                string
		roleARN             string
		validateCredentials func(t *testing.T, g Gomega, credentials string)
	}{
		{
			name:    "When AWS role ARN is provided, it should generate correct credentials format",
			roleARN: "arn:aws:iam::123456789012:role/karpenter-role",
			validateCredentials: func(t *testing.T, g Gomega, credentials string) {
				t.Helper()
				g.Expect(credentials).To(ContainSubstring("[default]"))
				g.Expect(credentials).To(ContainSubstring("role_arn = arn:aws:iam::123456789012:role/karpenter-role"))
				g.Expect(credentials).To(ContainSubstring("web_identity_token_file = /var/run/secrets/openshift/serviceaccount/token"))
				g.Expect(credentials).To(ContainSubstring("sts_regional_endpoints = regional"))
			},
		},
		{
			name:    "When different role ARN format is provided, it should be included correctly",
			roleARN: "arn:aws:iam::999999999999:role/my-custom-karpenter-role",
			validateCredentials: func(t *testing.T, g Gomega, credentials string) {
				t.Helper()
				g.Expect(credentials).To(ContainSubstring("role_arn = arn:aws:iam::999999999999:role/my-custom-karpenter-role"))
			},
		},
		{
			name:    "When role ARN has path component, it should be preserved",
			roleARN: "arn:aws:iam::111111111111:role/path/to/role/karpenter",
			validateCredentials: func(t *testing.T, g Gomega, credentials string) {
				t.Helper()
				g.Expect(credentials).To(ContainSubstring("role_arn = arn:aws:iam::111111111111:role/path/to/role/karpenter"))
			},
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
					AutoNode: hyperv1.AutoNode{
						Provisioner: hyperv1.ProvisionerConfig{
							Karpenter: hyperv1.KarpenterConfig{
								Platform: hyperv1.AWSPlatform,
								AWS: hyperv1.KarpenterAWSConfig{
									RoleARN: tc.roleARN,
								},
							},
						},
					},
				},
			}

			cpContext := controlplanecomponent.WorkloadContext{
				Context: t.Context(),
				HCP:     hcp,
			}

			secret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "karpenter-credentials",
					Namespace: "test-namespace",
				},
			}

			err := adaptCredentialsSecret(cpContext, secret)
			g.Expect(err).ToNot(HaveOccurred())

			// Verify secret type
			g.Expect(secret.Type).To(Equal(corev1.SecretTypeOpaque))

			// Verify credentials data exists
			g.Expect(secret.Data).To(HaveKey("credentials"))
			credentials := string(secret.Data["credentials"])

			// Verify credentials content
			if tc.validateCredentials != nil {
				tc.validateCredentials(t, g, credentials)
			}

			// Verify role ARN is in credentials
			g.Expect(credentials).To(ContainSubstring(tc.roleARN))
		})
	}
}

func TestAdaptCredentialsSecretFormat(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	roleARN := "arn:aws:iam::123456789012:role/test-role"

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "test-namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			AutoNode: hyperv1.AutoNode{
				Provisioner: hyperv1.ProvisionerConfig{
					Karpenter: hyperv1.KarpenterConfig{
						Platform: hyperv1.AWSPlatform,
						AWS: hyperv1.KarpenterAWSConfig{
							RoleARN: roleARN,
						},
					},
				},
			},
		},
	}

	cpContext := controlplanecomponent.WorkloadContext{
		Context: t.Context(),
		HCP:     hcp,
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "karpenter-credentials",
			Namespace: "test-namespace",
		},
	}

	err := adaptCredentialsSecret(cpContext, secret)
	g.Expect(err).ToNot(HaveOccurred())

	credentials := string(secret.Data["credentials"])

	// Verify the exact format matches AWS credentials file format
	expectedTemplate := `[default]
	role_arn = %s
	web_identity_token_file = /var/run/secrets/openshift/serviceaccount/token
	sts_regional_endpoints = regional
`
	expected := fmt.Sprintf(expectedTemplate, roleARN)

	g.Expect(credentials).To(Equal(expected))
}
