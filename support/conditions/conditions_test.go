package conditions

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestExpectedHCConditions(t *testing.T) {
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
			name: "When Azure KMS KeyVaultAccess is Private on ARO HCP, it should expect ValidAzureKMSConfig True",
			hc: newAzureHC(true, &hyperv1.KMSSpec{
				Provider: hyperv1.AZURE,
				Azure: &hyperv1.AzureKMSSpec{
					KeyVaultAccess: hyperv1.AzureKeyVaultPrivate,
				},
			}),
			expectedStatus: metav1.ConditionTrue,
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

	for _, tc := range []struct {
		name              string
		managedIdentities bool
		access            hyperv1.AzureKeyVaultAccessType
		status            metav1.ConditionStatus
		reason, message   string
		expected          metav1.ConditionStatus
	}{
		{
			name:              "When an older ARO CPO explicitly skips private vault validation, it should accept the legacy Unknown condition",
			managedIdentities: true, access: hyperv1.AzureKeyVaultPrivate,
			status: metav1.ConditionUnknown, reason: hyperv1.StatusUnknownReason,
			message: "Private Key Vault endpoint is not reachable from the management cluster", expected: metav1.ConditionUnknown,
		},
		{
			name:              "When the new validator is waiting for its router, it should accept the pending Unknown condition",
			managedIdentities: true, access: hyperv1.AzureKeyVaultPrivate,
			status: metav1.ConditionUnknown, reason: hyperv1.PrivateKeyVaultValidationPendingReason,
			message: "Private Key Vault cannot be validated yet: router has no available replicas", expected: metav1.ConditionUnknown,
		},
		{
			name:              "When the probe fails, it should require True",
			managedIdentities: true, access: hyperv1.AzureKeyVaultPrivate,
			status: metav1.ConditionFalse, reason: hyperv1.AzureErrorReason,
			message: "failed to encrypt data using KMS", expected: metav1.ConditionTrue,
		},
		{
			name:              "When an Unknown carries an unrecognized reason, it should require True",
			managedIdentities: true, access: hyperv1.AzureKeyVaultPrivate,
			status: metav1.ConditionUnknown, reason: hyperv1.AzureErrorReason,
			message: "Private Key Vault endpoint is not reachable from the management cluster", expected: metav1.ConditionTrue,
		},
		{
			name:              "When a public vault reports the legacy Unknown, it should require True",
			managedIdentities: true, access: hyperv1.AzureKeyVaultPublic,
			status: metav1.ConditionUnknown, reason: hyperv1.StatusUnknownReason,
			message: "Private Key Vault endpoint is not reachable from the management cluster", expected: metav1.ConditionTrue,
		},
		{
			name:              "When a public vault reports the pending Unknown, it should require True",
			managedIdentities: true, access: hyperv1.AzureKeyVaultPublic,
			status: metav1.ConditionUnknown, reason: hyperv1.PrivateKeyVaultValidationPendingReason,
			message: "Private Key Vault cannot be validated yet: router has no available replicas", expected: metav1.ConditionTrue,
		},
		{
			name:   "When a self-managed cluster has the legacy condition, it should still require True",
			access: hyperv1.AzureKeyVaultPrivate,
			status: metav1.ConditionUnknown, reason: hyperv1.StatusUnknownReason,
			message: "Private Key Vault endpoint is not reachable from the management cluster", expected: metav1.ConditionTrue,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			hc := newAzureHC(tc.managedIdentities, &hyperv1.KMSSpec{
				Provider: hyperv1.AZURE,
				Azure:    &hyperv1.AzureKMSSpec{KeyVaultAccess: tc.access},
			})
			hc.Status.Conditions = []metav1.Condition{{
				Type: string(hyperv1.ValidAzureKMSConfig), Status: tc.status, Reason: tc.reason, Message: tc.message,
			}}
			g.Expect(ExpectedHCConditions(hc)[hyperv1.ValidAzureKMSConfig]).To(Equal(tc.expected))
			// The reason alone decides the expectation: rewording a message in a
			// backport must not silently change which conditions are tolerated.
			hc.Status.Conditions[0].Message = "some other wording entirely"
			g.Expect(ExpectedHCConditions(hc)[hyperv1.ValidAzureKMSConfig]).To(Equal(tc.expected))
		})
	}

	for _, tc := range []struct {
		name, version string
		expected      metav1.ConditionStatus
	}{
		{"When the control plane is 4.23, it should expect unknown credentials", "4.23.0", metav1.ConditionUnknown},
		{"When the control plane is 5.0, it should expect unknown credentials", "5.0.0", metav1.ConditionUnknown},
		{"When the control plane is a 5.1 prerelease, it should require validation", "5.1.0-0.ci-20260909", metav1.ConditionTrue},
		{"When the control plane is later, it should require validation", "6.0.0", metav1.ConditionTrue},
		{"When the version is empty, it should require validation", "", metav1.ConditionTrue},
		{"When the version is malformed, it should require validation", "invalid", metav1.ConditionTrue},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			hc := &hyperv1.HostedCluster{}
			hc.Spec.Platform.Type = hyperv1.GCPPlatform
			hc.Status.ControlPlaneVersion.Desired.Version = tc.version
			expected := ExpectedHCConditions(hc)
			g.Expect(expected[hyperv1.ValidGCPWorkloadIdentity]).To(Equal(tc.expected))
			g.Expect(expected[hyperv1.ValidGCPCredentials]).To(Equal(tc.expected))
			g.Expect(expected[hyperv1.GCPEndpointAvailable]).To(Equal(metav1.ConditionTrue))
		})
	}
	t.Run("When the control plane upgrades from 5.0 to 5.1, it should refresh credential expectations", func(t *testing.T) {
		g := NewWithT(t)
		hc := &hyperv1.HostedCluster{}
		hc.Spec.Platform.Type = hyperv1.GCPPlatform
		hc.Status.ControlPlaneVersion.Desired.Version = "5.0.0"
		g.Expect(ExpectedHCConditions(hc)[hyperv1.ValidGCPCredentials]).To(Equal(metav1.ConditionUnknown))
		hc.Status.ControlPlaneVersion.Desired.Version = "5.1.0"
		g.Expect(ExpectedHCConditions(hc)[hyperv1.ValidGCPCredentials]).To(Equal(metav1.ConditionTrue))
	})
}
