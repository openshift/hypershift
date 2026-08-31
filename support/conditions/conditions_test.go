package conditions

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestExpectedHCConditions_ValidAzureKMSConfig(t *testing.T) {
	newAzureHC := func(managedIdentities bool, kms *hyperv1.KMSSpec) *hyperv1.HostedCluster {
		authType := hyperv1.AzureAuthenticationTypeWorkloadIdentities
		if managedIdentities {
			authType = hyperv1.AzureAuthenticationTypeManagedIdentities
		}
		hc := &hyperv1.HostedCluster{
			Spec: hyperv1.HostedClusterSpec{
				Platform: hyperv1.PlatformSpec{
					Type: hyperv1.AzurePlatform,
					Azure: &hyperv1.AzurePlatformSpec{
						AzureAuthenticationConfig: hyperv1.AzureAuthenticationConfiguration{
							AzureAuthenticationConfigType: authType,
						},
					},
				},
			},
		}
		if kms != nil {
			hc.Spec.SecretEncryption = &hyperv1.SecretEncryptionSpec{
				Type: hyperv1.KMS,
				KMS:  kms,
			}
		}
		return hc
	}

	tests := []struct {
		name           string
		hc             *hyperv1.HostedCluster
		expectedStatus metav1.ConditionStatus
	}{
		{
			name:           "When Azure KMS is not configured, it should expect ValidAzureKMSConfig Unknown",
			hc:             newAzureHC(true, nil),
			expectedStatus: metav1.ConditionUnknown,
		},
		{
			name: "When Azure KMS KeyVaultAccess is Private on ARO HCP, it should expect ValidAzureKMSConfig Unknown",
			hc: newAzureHC(true, &hyperv1.KMSSpec{
				Provider: hyperv1.AZURE,
				Azure: &hyperv1.AzureKMSSpec{
					KeyVaultAccess: hyperv1.AzureKeyVaultPrivate,
				},
			}),
			expectedStatus: metav1.ConditionUnknown,
		},
		{
			name: "When Azure KMS KeyVaultAccess is Public on ARO HCP, it should expect ValidAzureKMSConfig True",
			hc: newAzureHC(true, &hyperv1.KMSSpec{
				Provider: hyperv1.AZURE,
				Azure: &hyperv1.AzureKMSSpec{
					KeyVaultAccess: hyperv1.AzureKeyVaultPublic,
				},
			}),
			expectedStatus: metav1.ConditionTrue,
		},
		{
			name: "When Azure KMS KeyVaultAccess is Private on self-managed Azure, it should expect ValidAzureKMSConfig True",
			hc: newAzureHC(false, &hyperv1.KMSSpec{
				Provider: hyperv1.AZURE,
				Azure: &hyperv1.AzureKMSSpec{
					KeyVaultAccess: hyperv1.AzureKeyVaultPrivate,
				},
			}),
			expectedStatus: metav1.ConditionTrue,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			got := ExpectedHCConditions(tc.hc)
			g.Expect(got[hyperv1.ValidAzureKMSConfig]).To(Equal(tc.expectedStatus))
		})
	}
}
