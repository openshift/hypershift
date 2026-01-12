package azure

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestValidate(t *testing.T) {
	tests := map[string]struct {
		opts          CreateInfraOptions
		expectedError bool
		errorContains string
		description   string
	}{
		"When base domain is provided in normal mode it should pass validation": {
			opts: CreateInfraOptions{
				BaseDomain: "example.com",
			},
			expectedError: false,
			description:   "Should pass when base domain is provided",
		},
		"When base domain is missing in normal mode it should return an error": {
			opts:          CreateInfraOptions{},
			expectedError: true,
			errorContains: "--base-domain is required",
			description:   "Should require base domain when not in generate mode",
		},
		"When base domain is missing in generate mode it should pass validation": {
			opts: CreateInfraOptions{
				GenerateManagedIdentities: true,
			},
			expectedError: false,
			description:   "Should not require base domain when in generate mode",
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

func TestGenerateModeValidation(t *testing.T) {
	tests := map[string]struct {
		opts          CreateInfraOptions
		expectedError bool
		errorContains string
		description   string
	}{
		"When all required generate mode flags are provided it should pass validation": {
			opts: CreateInfraOptions{
				GenerateManagedIdentities:    true,
				OIDCIssuerURL:                "https://issuer.example.com",
				WorkloadIdentitiesOutputFile: "/path/to/output.json",
				ResourceGroupName:            "my-resource-group",
			},
			expectedError: false,
			description:   "Should pass when all required generate mode flags are provided",
		},
		"When OIDC issuer URL is missing in generate mode it should return an error": {
			opts: CreateInfraOptions{
				GenerateManagedIdentities:    true,
				WorkloadIdentitiesOutputFile: "/path/to/output.json",
				ResourceGroupName:            "my-resource-group",
			},
			expectedError: true,
			errorContains: "--oidc-issuer-url is required",
			description:   "Should require OIDC issuer URL in generate mode",
		},
		"When output file is missing in generate mode it should return an error": {
			opts: CreateInfraOptions{
				GenerateManagedIdentities: true,
				OIDCIssuerURL:             "https://issuer.example.com",
				ResourceGroupName:         "my-resource-group",
			},
			expectedError: true,
			errorContains: "--workload-identities-output-file is required",
			description:   "Should require output file in generate mode",
		},
		"When resource group name is missing in generate mode it should return an error": {
			opts: CreateInfraOptions{
				GenerateManagedIdentities:    true,
				OIDCIssuerURL:                "https://issuer.example.com",
				WorkloadIdentitiesOutputFile: "/path/to/output.json",
			},
			expectedError: true,
			errorContains: "--resource-group-name is required",
			description:   "Should require resource group name in generate mode",
		},
		"When managed identities file is provided in generate mode it should return an error": {
			opts: CreateInfraOptions{
				GenerateManagedIdentities:    true,
				OIDCIssuerURL:                "https://issuer.example.com",
				WorkloadIdentitiesOutputFile: "/path/to/output.json",
				ResourceGroupName:            "my-resource-group",
				ManagedIdentitiesFile:        "mi.json",
			},
			expectedError: true,
			errorContains: "--generate-managed-identities cannot be used with",
			description:   "Should reject generate mode with managed identities file",
		},
		"When data plane identities file is provided in generate mode it should return an error": {
			opts: CreateInfraOptions{
				GenerateManagedIdentities:    true,
				OIDCIssuerURL:                "https://issuer.example.com",
				WorkloadIdentitiesOutputFile: "/path/to/output.json",
				ResourceGroupName:            "my-resource-group",
				DataPlaneIdentitiesFile:      "dp.json",
			},
			expectedError: true,
			errorContains: "--generate-managed-identities cannot be used with",
			description:   "Should reject generate mode with data plane identities file",
		},
		"When workload identities file is provided in generate mode it should return an error": {
			opts: CreateInfraOptions{
				GenerateManagedIdentities:    true,
				OIDCIssuerURL:                "https://issuer.example.com",
				WorkloadIdentitiesOutputFile: "/path/to/output.json",
				ResourceGroupName:            "my-resource-group",
				WorkloadIdentitiesFile:       "wi.json",
			},
			expectedError: true,
			errorContains: "--generate-managed-identities cannot be used with",
			description:   "Should reject generate mode with workload identities file",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			err := test.opts.validateDeploymentModelFlags()

			if test.expectedError {
				g.Expect(err).ToNot(BeNil(), test.description)
				g.Expect(err.Error()).To(ContainSubstring(test.errorContains), test.description)
			} else {
				g.Expect(err).To(BeNil(), test.description)
			}
		})
	}
}

func TestValidateDeploymentModelFlags(t *testing.T) {
	tests := map[string]struct {
		opts          CreateInfraOptions
		expectedError bool
		description   string
	}{
		"When both ARO HCP managed and data plane identities are provided it should pass validation": {
			opts: CreateInfraOptions{
				ManagedIdentitiesFile:   "mi.json",
				DataPlaneIdentitiesFile: "dp.json",
			},
			expectedError: false,
			description:   "Should allow both ARO HCP identity files together",
		},
		"When workload identities file is provided alone it should pass validation": {
			opts: CreateInfraOptions{
				WorkloadIdentitiesFile: "wi.json",
			},
			expectedError: false,
			description:   "Should allow workload identities file alone",
		},
		"When OIDC issuer URL is provided alone it should pass validation": {
			opts: CreateInfraOptions{
				OIDCIssuerURL: "https://issuer.com",
			},
			expectedError: false,
			description:   "Should allow OIDC issuer URL alone",
		},
		"When no identity configuration is provided it should return an error": {
			opts:          CreateInfraOptions{},
			expectedError: true,
			description:   "Should reject empty configuration without any identity settings",
		},
		"When ARO HCP managed identities are mixed with workload identities it should return an error": {
			opts: CreateInfraOptions{
				ManagedIdentitiesFile:  "mi.json",
				WorkloadIdentitiesFile: "wi.json",
			},
			expectedError: true,
			description:   "Should reject mixing ARO HCP managed identities with self-managed workload identities",
		},
		"When ARO HCP managed identities are used with OIDC issuer it should pass validation": {
			opts: CreateInfraOptions{
				ManagedIdentitiesFile: "mi.json",
				OIDCIssuerURL:         "https://issuer.com",
			},
			expectedError: false,
			description:   "Should allow ARO HCP managed identities with OIDC issuer",
		},
		"When ARO HCP data plane identities are mixed with workload identities it should return an error": {
			opts: CreateInfraOptions{
				DataPlaneIdentitiesFile: "dp.json",
				WorkloadIdentitiesFile:  "wi.json",
			},
			expectedError: true,
			description:   "Should reject mixing ARO HCP data plane identities with self-managed workload identities",
		},
		"When ARO HCP data plane identities are used with OIDC issuer it should pass validation": {
			opts: CreateInfraOptions{
				DataPlaneIdentitiesFile: "dp.json",
				OIDCIssuerURL:           "https://issuer.com",
			},
			expectedError: false,
			description:   "Should allow ARO HCP data plane identities with OIDC issuer",
		},
		"When workload identities file is used with OIDC issuer it should return an error": {
			opts: CreateInfraOptions{
				WorkloadIdentitiesFile: "wi.json",
				OIDCIssuerURL:          "https://issuer.com",
			},
			expectedError: true,
			description:   "Should reject mixing self-managed workload identities file with OIDC issuer URL",
		},
		"When all deployment models are mixed together it should return an error": {
			opts: CreateInfraOptions{
				ManagedIdentitiesFile:   "mi.json",
				DataPlaneIdentitiesFile: "dp.json",
				WorkloadIdentitiesFile:  "wi.json",
				OIDCIssuerURL:           "https://issuer.com",
			},
			expectedError: true,
			description:   "Should reject mixing all deployment models together",
		},
		"When managed identities and workload identities and OIDC issuer conflict it should return an error": {
			opts: CreateInfraOptions{
				ManagedIdentitiesFile:  "mi.json",
				WorkloadIdentitiesFile: "wi.json",
				OIDCIssuerURL:          "https://issuer.com",
			},
			expectedError: true,
			description:   "Should reject triple conflict between managed identities, workload identities, and OIDC issuer",
		},
		"When data plane identities and workload identities and OIDC issuer conflict it should return an error": {
			opts: CreateInfraOptions{
				DataPlaneIdentitiesFile: "dp.json",
				WorkloadIdentitiesFile:  "wi.json",
				OIDCIssuerURL:           "https://issuer.com",
			},
			expectedError: true,
			description:   "Should reject triple conflict between data plane identities, workload identities, and OIDC issuer",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			err := test.opts.validateDeploymentModelFlags()

			if test.expectedError {
				g.Expect(err).To(Not(BeNil()), test.description)
				// Verify the error mentions either mutual exclusion or missing configuration
				g.Expect(err.Error()).To(SatisfyAny(
					ContainSubstring("mutually exclusive"),
					ContainSubstring("at least one identity configuration must be provided"),
				), "Error should mention validation failure: %s", test.description)
			} else {
				g.Expect(err).To(BeNil(), test.description)
			}
		})
	}
}
