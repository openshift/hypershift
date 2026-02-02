package azure

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestNewIdentityManager(t *testing.T) {
	tests := map[string]struct {
		subscriptionID string
		cloud          string
	}{
		"When valid subscription ID is provided it should create identity manager": {
			subscriptionID: "12345678-1234-1234-1234-123456789012",
			cloud:          "AzurePublicCloud",
		},
		"When different subscription ID is provided it should create identity manager with that ID": {
			subscriptionID: "abcdefgh-abcd-abcd-abcd-abcdefghijkl",
			cloud:          "AzureUSGovernmentCloud",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			// Create with nil credentials since we're just testing the constructor
			manager := NewIdentityManager(test.subscriptionID, nil, test.cloud)

			g.Expect(manager).ToNot(BeNil())
			g.Expect(manager.subscriptionID).To(Equal(test.subscriptionID))
			g.Expect(manager.creds).To(BeNil())
			g.Expect(manager.cloud).To(Equal(test.cloud))
		})
	}
}

func TestGetWorkloadIdentityDefinitions(t *testing.T) {
	tests := map[string]struct {
		clusterName       string
		expectedCount     int
		expectedComponent []string
	}{
		"When called it should return 7 identity definitions with correct components": {
			clusterName:   "test-cluster",
			expectedCount: 7,
			expectedComponent: []string{
				"disk",
				"file",
				"imageRegistry",
				"ingress",
				"cloudProvider",
				"nodePoolManagement",
				"network",
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			definitions := GetWorkloadIdentityDefinitions(test.clusterName)

			// Verify count
			g.Expect(definitions).To(HaveLen(test.expectedCount), "Should return 7 identity definitions")

			// Verify all expected components are present
			componentNames := make([]string, len(definitions))
			for i, def := range definitions {
				componentNames[i] = def.ComponentName
			}

			for _, expectedComponent := range test.expectedComponent {
				g.Expect(componentNames).To(ContainElement(expectedComponent), "Should contain "+expectedComponent+" component")
			}

			// Verify each definition has at least one federated credential
			for _, def := range definitions {
				g.Expect(def.FederatedCredentials).ToNot(BeEmpty(), "Definition for %s should have at least one federated credential", def.ComponentName)
				g.Expect(def.IdentityNameSuffix).ToNot(BeEmpty(), "Definition for %s should have an identity name suffix", def.ComponentName)
			}
		})
	}
}

func TestFederatedCredentialConfig(t *testing.T) {
	tests := map[string]struct {
		credentialName string
		subject        string
		audience       string
	}{
		"When disk driver node service account is configured it should store correct values": {
			credentialName: "test-disk-fed-id-node",
			subject:        "system:serviceaccount:openshift-cluster-csi-drivers:azure-disk-csi-driver-node-sa",
			audience:       "openshift",
		},
		"When ingress operator service account is configured it should store correct values": {
			credentialName: "test-ingress-fed-id",
			subject:        "system:serviceaccount:openshift-ingress-operator:ingress-operator",
			audience:       "openshift",
		},
		"When cloud provider service account is configured it should store correct values": {
			credentialName: "test-cloud-provider-fed-id",
			subject:        "system:serviceaccount:kube-system:azure-cloud-provider",
			audience:       "openshift",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			config := FederatedCredentialConfig{
				CredentialName: test.credentialName,
				Subject:        test.subject,
				Audience:       test.audience,
			}

			g.Expect(config.CredentialName).To(Equal(test.credentialName))
			g.Expect(config.Subject).To(Equal(test.subject))
			g.Expect(config.Audience).To(Equal(test.audience))
		})
	}
}
