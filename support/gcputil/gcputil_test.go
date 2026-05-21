package gcputil

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

const (
	testImageRegistryGSA = "image-registry@test-project.iam.gserviceaccount.com"
	testProjectNumber    = "123456789012"
	testPoolID           = "test-pool"
	testProviderID       = "test-provider"
)

func TestBuildWorkloadIdentityCredentials(t *testing.T) {
	t.Parallel()
	g := NewWithT(t)

	wif := hyperv1.GCPWorkloadIdentityConfig{
		ProjectNumber: testProjectNumber,
		PoolID:        testPoolID,
		ProviderID:    testProviderID,
	}

	credentials, err := BuildWorkloadIdentityCredentials(wif, testImageRegistryGSA)
	g.Expect(err).ToNot(HaveOccurred())
	g.Expect(credentials).To(ContainSubstring(`"type":"external_account"`))
	g.Expect(credentials).To(ContainSubstring(testProjectNumber))
	g.Expect(credentials).To(ContainSubstring(testPoolID))
	g.Expect(credentials).To(ContainSubstring(testProviderID))
	g.Expect(credentials).To(ContainSubstring(testImageRegistryGSA))
	g.Expect(credentials).To(ContainSubstring("/var/run/secrets/openshift/serviceaccount/token"))
}

func TestBuildWorkloadIdentityCredentialsValidation(t *testing.T) {
	t.Parallel()
	validWIF := func() hyperv1.GCPWorkloadIdentityConfig {
		return hyperv1.GCPWorkloadIdentityConfig{
			ProjectNumber: testProjectNumber,
			PoolID:        testPoolID,
			ProviderID:    testProviderID,
		}
	}

	tests := []struct {
		name                string
		mutateWIF           func(*hyperv1.GCPWorkloadIdentityConfig)
		serviceAccountEmail string
		errorMsg            string
	}{
		{
			name:                "When all fields are valid it should succeed",
			serviceAccountEmail: testImageRegistryGSA,
		},
		{
			name:                "When project number is empty it should return an error",
			mutateWIF:           func(wif *hyperv1.GCPWorkloadIdentityConfig) { wif.ProjectNumber = "" },
			serviceAccountEmail: testImageRegistryGSA,
			errorMsg:            "project number cannot be empty",
		},
		{
			name:                "When pool ID is empty it should return an error",
			mutateWIF:           func(wif *hyperv1.GCPWorkloadIdentityConfig) { wif.PoolID = "" },
			serviceAccountEmail: testImageRegistryGSA,
			errorMsg:            "pool ID cannot be empty",
		},
		{
			name:                "When provider ID is empty it should return an error",
			mutateWIF:           func(wif *hyperv1.GCPWorkloadIdentityConfig) { wif.ProviderID = "" },
			serviceAccountEmail: testImageRegistryGSA,
			errorMsg:            "provider ID cannot be empty",
		},
		{
			name:                "When service account email is empty it should return an error",
			serviceAccountEmail: "",
			errorMsg:            "service account email cannot be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			wif := validWIF()
			if tt.mutateWIF != nil {
				tt.mutateWIF(&wif)
			}
			_, err := BuildWorkloadIdentityCredentials(wif, tt.serviceAccountEmail)
			if tt.errorMsg != "" {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tt.errorMsg))
			} else {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}
