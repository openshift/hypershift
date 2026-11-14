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
	t.Run("When GCP resource tags are configured, it should require valid configuration", func(t *testing.T) {
		g := NewWithT(t)
		hc := &hyperv1.HostedCluster{}
		hc.Spec.Platform.Type = hyperv1.GCPPlatform
		hc.Spec.Platform.GCP = &hyperv1.GCPPlatformSpec{ResourceTags: []hyperv1.GCPResourceTag{{Key: "environment", Value: "production"}}}

		g.Expect(ExpectedHCConditions(hc)[hyperv1.ValidHostedClusterConfiguration]).To(Equal(metav1.ConditionTrue))
	})
	t.Run("When the control plane upgrades from 5.0 to 5.1, it should refresh credential expectations", func(t *testing.T) {
		g := NewWithT(t)
		hc := &hyperv1.HostedCluster{}
		hc.Spec.Platform.Type = hyperv1.GCPPlatform
		hc.Status.ControlPlaneVersion.Desired.Version = "5.0.0"
		g.Expect(ExpectedHCConditions(hc)[hyperv1.ValidGCPCredentials]).To(Equal(metav1.ConditionUnknown))
		hc.Status.ControlPlaneVersion.Desired.Version = "5.1.0"
		g.Expect(ExpectedHCConditions(hc)[hyperv1.ValidGCPCredentials]).To(Equal(metav1.ConditionTrue))
	})

	newKubeVirtHC := func(managementPlatform string, networkType hyperv1.NetworkType, credentials *hyperv1.KubevirtPlatformCredentials) *hyperv1.HostedCluster {
		hc := &hyperv1.HostedCluster{
			Spec: hyperv1.HostedClusterSpec{
				Networking: hyperv1.ClusterNetworking{
					NetworkType: networkType,
				},
				Platform: hyperv1.PlatformSpec{
					Type: hyperv1.KubevirtPlatform,
					Kubevirt: &hyperv1.KubevirtPlatformSpec{
						Credentials: credentials,
					},
				},
			},
		}
		if managementPlatform != "" {
			hc.Annotations = map[string]string{hyperv1.ManagementPlatformAnnotation: managementPlatform}
		}
		return hc
	}

	// hasCondition reports whether the condition is expected at all, which is distinct from
	// being expected with a False status.
	hasCondition := func(conditions map[hyperv1.ConditionType]metav1.ConditionStatus, conditionType hyperv1.ConditionType) bool {
		_, ok := conditions[conditionType]
		return ok
	}

	t.Run("When KubeVirt runs on an AWS management cluster, it should expect jumbo frames and non-live-migratable nodes", func(t *testing.T) {
		g := NewWithT(t)
		got := ExpectedHCConditions(newKubeVirtHC(string(hyperv1.AWSPlatform), hyperv1.OVNKubernetes, nil))
		g.Expect(got[hyperv1.ValidKubeVirtInfraNetworkMTU]).To(Equal(metav1.ConditionTrue))
		// the CI storage class only offers RWO, so the VMs cannot live migrate
		g.Expect(got[hyperv1.KubeVirtNodesLiveMigratable]).To(Equal(metav1.ConditionFalse))
	})

	t.Run("When KubeVirt runs on an Azure management cluster, it should not expect jumbo frames nor the live migratable condition", func(t *testing.T) {
		g := NewWithT(t)
		got := ExpectedHCConditions(newKubeVirtHC(string(hyperv1.AzurePlatform), hyperv1.OVNKubernetes, nil))
		g.Expect(got[hyperv1.ValidKubeVirtInfraNetworkMTU]).To(Equal(metav1.ConditionFalse))
		g.Expect(hasCondition(got, hyperv1.KubeVirtNodesLiveMigratable)).To(BeFalse())
	})

	t.Run("When KubeVirt has no management platform annotation, it should expect neither the MTU nor the live migratable condition", func(t *testing.T) {
		g := NewWithT(t)
		got := ExpectedHCConditions(newKubeVirtHC("", hyperv1.OVNKubernetes, nil))
		g.Expect(hasCondition(got, hyperv1.ValidKubeVirtInfraNetworkMTU)).To(BeFalse())
		g.Expect(hasCondition(got, hyperv1.KubeVirtNodesLiveMigratable)).To(BeFalse())
	})

	t.Run("When KubeVirt does not use OVNKubernetes on AWS, it should skip the MTU condition but still expect non-live-migratable nodes", func(t *testing.T) {
		g := NewWithT(t)
		got := ExpectedHCConditions(newKubeVirtHC(string(hyperv1.AWSPlatform), hyperv1.Other, nil))
		g.Expect(hasCondition(got, hyperv1.ValidKubeVirtInfraNetworkMTU)).To(BeFalse())
		g.Expect(got[hyperv1.KubeVirtNodesLiveMigratable]).To(Equal(metav1.ConditionFalse))
	})

	t.Run("When KubeVirt external infra credentials are configured, it should expect the network policy RBAC condition", func(t *testing.T) {
		g := NewWithT(t)
		credentials := &hyperv1.KubevirtPlatformCredentials{InfraNamespace: "kubevirt-infra"}
		got := ExpectedHCConditions(newKubeVirtHC(string(hyperv1.AWSPlatform), hyperv1.OVNKubernetes, credentials))
		g.Expect(got[hyperv1.ValidKubeVirtInfraNetworkPolicyRBAC]).To(Equal(metav1.ConditionTrue))
	})

	t.Run("When KubeVirt external infra credentials are absent, it should not expect the network policy RBAC condition", func(t *testing.T) {
		g := NewWithT(t)
		got := ExpectedHCConditions(newKubeVirtHC(string(hyperv1.AWSPlatform), hyperv1.OVNKubernetes, nil))
		g.Expect(hasCondition(got, hyperv1.ValidKubeVirtInfraNetworkPolicyRBAC)).To(BeFalse())
	})
}
