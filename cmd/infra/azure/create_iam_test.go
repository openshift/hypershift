package azure

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestCreateIAMOptionsValidate(t *testing.T) {
	tests := map[string]struct {
		opts          CreateIAMOptions
		expectedError bool
		errorContains string
		description   string
	}{
		"When all required fields are provided it should pass validation": {
			opts: CreateIAMOptions{
				Name:              "test-cluster",
				InfraID:           "test-infra-id",
				CredentialsFile:   "/path/to/creds.json",
				ResourceGroupName: "test-rg",
				OIDCIssuerURL:     "https://issuer.example.com",
				OutputFile:        "/path/to/output.json",
			},
			expectedError: false,
			description:   "Should pass when all required fields are provided",
		},
		"When name is empty it should return an error": {
			opts: CreateIAMOptions{
				InfraID:           "test-infra-id",
				CredentialsFile:   "/path/to/creds.json",
				ResourceGroupName: "test-rg",
				OIDCIssuerURL:     "https://issuer.example.com",
				OutputFile:        "/path/to/output.json",
			},
			expectedError: true,
			errorContains: "name is required",
			description:   "Should require name",
		},
		"When infra-id is empty it should return an error": {
			opts: CreateIAMOptions{
				Name:              "test-cluster",
				CredentialsFile:   "/path/to/creds.json",
				ResourceGroupName: "test-rg",
				OIDCIssuerURL:     "https://issuer.example.com",
				OutputFile:        "/path/to/output.json",
			},
			expectedError: true,
			errorContains: "infra-id is required",
			description:   "Should require infra-id",
		},
		"When azure-creds is empty it should return an error": {
			opts: CreateIAMOptions{
				Name:              "test-cluster",
				InfraID:           "test-infra-id",
				ResourceGroupName: "test-rg",
				OIDCIssuerURL:     "https://issuer.example.com",
				OutputFile:        "/path/to/output.json",
			},
			expectedError: true,
			errorContains: "azure-creds is required",
			description:   "Should require azure-creds",
		},
		"When resource-group-name is empty it should return an error": {
			opts: CreateIAMOptions{
				Name:            "test-cluster",
				InfraID:         "test-infra-id",
				CredentialsFile: "/path/to/creds.json",
				OIDCIssuerURL:   "https://issuer.example.com",
				OutputFile:      "/path/to/output.json",
			},
			expectedError: true,
			errorContains: "resource-group-name is required",
			description:   "Should require resource-group-name",
		},
		"When oidc-issuer-url is empty it should return an error": {
			opts: CreateIAMOptions{
				Name:              "test-cluster",
				InfraID:           "test-infra-id",
				CredentialsFile:   "/path/to/creds.json",
				ResourceGroupName: "test-rg",
				OutputFile:        "/path/to/output.json",
			},
			expectedError: true,
			errorContains: "oidc-issuer-url is required",
			description:   "Should require oidc-issuer-url",
		},
		"When output-file is empty it should return an error": {
			opts: CreateIAMOptions{
				Name:              "test-cluster",
				InfraID:           "test-infra-id",
				CredentialsFile:   "/path/to/creds.json",
				ResourceGroupName: "test-rg",
				OIDCIssuerURL:     "https://issuer.example.com",
			},
			expectedError: true,
			errorContains: "output-file is required",
			description:   "Should require output-file",
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

func TestDefaultCreateIAMOptions(t *testing.T) {
	g := NewGomegaWithT(t)

	opts := DefaultCreateIAMOptions()

	g.Expect(opts.Location).To(Equal("eastus"), "Should have default location")
	g.Expect(opts.Cloud).To(Equal("AzurePublicCloud"), "Should have default cloud")
}
