package azure

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestDestroyIAMOptionsValidate(t *testing.T) {
	tests := map[string]struct {
		opts          DestroyIAMOptions
		expectedError bool
		errorContains string
		description   string
	}{
		"When all required fields are provided it should pass validation": {
			opts: DestroyIAMOptions{
				Name:                   "test-cluster",
				InfraID:                "test-infra-id",
				WorkloadIdentitiesFile: "/path/to/identities.json",
				CredentialsFile:        "/path/to/creds.json",
				ResourceGroupName:      "test-rg",
			},
			expectedError: false,
			description:   "Should pass when all required fields are provided",
		},
		"When workload-identities-file is empty it should return an error": {
			opts: DestroyIAMOptions{
				Name:              "test-cluster",
				InfraID:           "test-infra-id",
				CredentialsFile:   "/path/to/creds.json",
				ResourceGroupName: "test-rg",
			},
			expectedError: true,
			errorContains: "workload-identities-file is required",
			description:   "Should require workload-identities-file",
		},
		"When azure-creds is empty it should return an error": {
			opts: DestroyIAMOptions{
				Name:                   "test-cluster",
				InfraID:                "test-infra-id",
				WorkloadIdentitiesFile: "/path/to/identities.json",
				ResourceGroupName:      "test-rg",
			},
			expectedError: true,
			errorContains: "azure-creds is required",
			description:   "Should require azure-creds",
		},
		"When resource-group-name is empty it should return an error": {
			opts: DestroyIAMOptions{
				Name:                   "test-cluster",
				InfraID:                "test-infra-id",
				WorkloadIdentitiesFile: "/path/to/identities.json",
				CredentialsFile:        "/path/to/creds.json",
			},
			expectedError: true,
			errorContains: "resource-group-name is required",
			description:   "Should require resource-group-name",
		},
		"When name is empty it should return an error": {
			opts: DestroyIAMOptions{
				InfraID:                "test-infra-id",
				WorkloadIdentitiesFile: "/path/to/identities.json",
				CredentialsFile:        "/path/to/creds.json",
				ResourceGroupName:      "test-rg",
			},
			expectedError: true,
			errorContains: "name is required",
			description:   "Should require name",
		},
		"When infra-id is empty it should return an error": {
			opts: DestroyIAMOptions{
				Name:                   "test-cluster",
				WorkloadIdentitiesFile: "/path/to/identities.json",
				CredentialsFile:        "/path/to/creds.json",
				ResourceGroupName:      "test-rg",
			},
			expectedError: true,
			errorContains: "infra-id is required",
			description:   "Should require infra-id",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			err := test.opts.Validate()

			if test.expectedError {
				g.Expect(err).ToNot(BeNil(), test.description)
				g.Expect(err.Error()).To(ContainSubstring(test.errorContains), test.description)
			} else {
				g.Expect(err).To(BeNil(), test.description)
			}
		})
	}
}

func TestDefaultDestroyIAMOptions(t *testing.T) {
	g := NewGomegaWithT(t)

	opts := DefaultDestroyIAMOptions()

	g.Expect(opts.Cloud).To(Equal("AzurePublicCloud"), "Should have default cloud")
}
