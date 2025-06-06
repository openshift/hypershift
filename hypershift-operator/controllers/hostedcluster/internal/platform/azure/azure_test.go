package azure

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/config"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	capiazure "sigs.k8s.io/cluster-api-provider-azure/api/v1beta1"

	"github.com/blang/semver"
)

func TestReconcileAzureClusterIdentity(t *testing.T) {
	// Set up initial conditions
	hcVersion := semver.MustParse("4.19.0")
	controlPlaneNamespace := "test-namespace"
	initialAzureClusterIdentity := &capiazure.AzureClusterIdentity{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cluster-identity",
			Namespace: controlPlaneNamespace,
		},
	}

	testCases := []struct {
		name                         string
		isManagedService             bool
		hc                           *hyperv1.HostedCluster
		expectedAzureClusterIdentity *capiazure.AzureClusterIdentity
	}{
		{
			name:             "when MANAGED_SERVICE is set to AROHCP, it should reconcile AzureClusterIdentity as UserAssignedIdentityCredential",
			isManagedService: true,
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hc",
					Namespace: controlPlaneNamespace,
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Azure: &hyperv1.AzurePlatformSpec{
							TenantID: "test-tenant-id",
							Cloud:    "AzurePublicCloud",
							ManagedIdentities: hyperv1.AzureResourceManagedIdentities{
								ControlPlane: hyperv1.ControlPlaneManagedIdentities{
									NodePoolManagement: hyperv1.ManagedIdentity{
										CredentialsSecretName: "credentials",
									},
								},
							},
						},
					},
				},
			},
			expectedAzureClusterIdentity: &capiazure.AzureClusterIdentity{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster-identity",
					Namespace: controlPlaneNamespace,
				},
				Spec: capiazure.AzureClusterIdentitySpec{
					TenantID:                                 "test-tenant-id",
					UserAssignedIdentityCredentialsCloudType: "public",
					UserAssignedIdentityCredentialsPath:      config.ManagedAzureCertificatePath + "credentials",
					Type:                                     capiazure.UserAssignedIdentityCredential,
				},
			},
		},
		{
			name:             "when MANAGED_SERVICE is not set, it should reconcile AzureClusterIdentity as WorkloadIdentity",
			isManagedService: false,
			hc: &hyperv1.HostedCluster{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hc",
					Namespace: controlPlaneNamespace,
				},
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{
						Azure: &hyperv1.AzurePlatformSpec{
							TenantID: "test-tenant-id",
							Cloud:    "AzurePublicCloud",
							WorkloadIdentities: &hyperv1.AzureWorkloadIdentities{
								CAPZClientID: "test-client-id",
							},
						},
					},
				},
			},
			expectedAzureClusterIdentity: &capiazure.AzureClusterIdentity{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-cluster-identity",
					Namespace: controlPlaneNamespace,
				},
				Spec: capiazure.AzureClusterIdentitySpec{
					ClientID: "test-client-id",
					TenantID: "test-tenant-id",
					Type:     capiazure.WorkloadIdentity,
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			if tc.isManagedService {
				t.Setenv("MANAGED_SERVICE", hyperv1.AroHCP)
			}

			err := reconcileAzureClusterIdentity(tc.hc, initialAzureClusterIdentity, controlPlaneNamespace, &hcVersion)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(initialAzureClusterIdentity.Spec.TenantID).Should(Equal(tc.expectedAzureClusterIdentity.Spec.TenantID))
			g.Expect(initialAzureClusterIdentity.Spec.Type).Should(Equal(tc.expectedAzureClusterIdentity.Spec.Type))

			if tc.isManagedService {
				g.Expect(initialAzureClusterIdentity.Spec.UserAssignedIdentityCredentialsPath).Should(Equal(tc.expectedAzureClusterIdentity.Spec.UserAssignedIdentityCredentialsPath))
				g.Expect(initialAzureClusterIdentity.Spec.UserAssignedIdentityCredentialsCloudType).Should(Equal(tc.expectedAzureClusterIdentity.Spec.UserAssignedIdentityCredentialsCloudType))
			} else {
				g.Expect(initialAzureClusterIdentity.Spec.UserAssignedIdentityCredentialsPath).Should(BeEmpty())
				g.Expect(initialAzureClusterIdentity.Spec.UserAssignedIdentityCredentialsCloudType).Should(BeEmpty())
				g.Expect(initialAzureClusterIdentity.Spec.ClientID).Should(Equal(tc.expectedAzureClusterIdentity.Spec.ClientID))
				g.Expect(initialAzureClusterIdentity.Spec.Type).Should(Equal(tc.expectedAzureClusterIdentity.Spec.Type))
			}
		})
	}
}

func TestParseCloudType(t *testing.T) {
	testCases := []struct {
		name           string
		input          string
		expectedOutput string
		expectedError  bool
	}{
		{
			name:           "when input is AzurePublicCloud, expected output is public",
			input:          "AzurePublicCloud",
			expectedOutput: "public",
			expectedError:  false,
		},
		{
			name:           "when input is AzureUSGovernmentCloud, expected output is usgovernment",
			input:          "AzureUSGovernmentCloud",
			expectedOutput: "usgovernment",
			expectedError:  false,
		},
		{
			name:           "when input is AzureChinaCloud, expected output is china",
			input:          "AzureChinaCloud",
			expectedOutput: "china",
			expectedError:  false,
		},
		{
			name:           "when input is an invalid cloud type, expect error",
			input:          "AzureGermanCloud",
			expectedOutput: "",
			expectedError:  true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			azureCloudType, err := parseCloudType(tc.input)
			g.Expect(azureCloudType).To(Equal(tc.expectedOutput))
			if tc.expectedError {
				g.Expect(err).To(HaveOccurred())
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
		})
	}
}
