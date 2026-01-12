package azure

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestNewIdentityManager(t *testing.T) {
	tests := map[string]struct {
		subscriptionID string
	}{
		"When valid subscription ID is provided it should create identity manager": {
			subscriptionID: "12345678-1234-1234-1234-123456789012",
		},
		"When different subscription ID is provided it should create identity manager with that ID": {
			subscriptionID: "abcdefgh-abcd-abcd-abcd-abcdefghijkl",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			// Create with nil credentials since we're just testing the constructor
			manager := NewIdentityManager(test.subscriptionID, nil)

			g.Expect(manager).ToNot(BeNil())
			g.Expect(manager.subscriptionID).To(Equal(test.subscriptionID))
			g.Expect(manager.creds).To(BeNil())
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
