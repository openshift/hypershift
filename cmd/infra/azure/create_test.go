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
		"When base domain is provided it should pass validation": {
			opts: CreateInfraOptions{
				BaseDomain: "example.com",
			},
			expectedError: false,
			description:   "Should pass when base domain is provided",
		},
		"When base domain is missing it should return an error": {
			opts:          CreateInfraOptions{},
			expectedError: true,
			errorContains: "--base-domain is required",
			description:   "Should require base domain",
		},
		"When assign-identity-roles is set without dns-zone-rg-name it should return an error": {
			opts: CreateInfraOptions{
				BaseDomain:                  "example.com",
				AssignServicePrincipalRoles: true,
				DNSZoneRG:                   "",
			},
			expectedError: true,
			errorContains: "--dns-zone-rg-name is required when --assign-identity-roles or --assign-custom-hcp-roles is set",
			description:   "Should require dns-zone-rg-name when assign-identity-roles is set",
		},
		"When assign-custom-hcp-roles is set without dns-zone-rg-name it should return an error": {
			opts: CreateInfraOptions{
				BaseDomain:           "example.com",
				AssignCustomHCPRoles: true,
				DNSZoneRG:            "",
			},
			expectedError: true,
			errorContains: "--dns-zone-rg-name is required when --assign-identity-roles or --assign-custom-hcp-roles is set",
			description:   "Should require dns-zone-rg-name when assign-custom-hcp-roles is set",
		},
		"When assign-identity-roles is set with dns-zone-rg-name it should pass validation": {
			opts: CreateInfraOptions{
				BaseDomain:                  "example.com",
				AssignServicePrincipalRoles: true,
				DNSZoneRG:                   "my-dns-rg",
			},
			expectedError: false,
			description:   "Should pass when both assign-identity-roles and dns-zone-rg-name are set",
		},
		"When assign-identity-roles is not set and dns-zone-rg-name is empty it should pass validation": {
			opts: CreateInfraOptions{
				BaseDomain:                  "example.com",
				AssignServicePrincipalRoles: false,
				DNSZoneRG:                   "",
			},
			expectedError: false,
			description:   "Should pass when assign-identity-roles is not set regardless of dns-zone-rg-name",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			err := test.opts.Validate()

			if test.expectedError {
				g.Expect(err).To(HaveOccurred(), test.description)
				g.Expect(err.Error()).To(ContainSubstring(test.errorContains), test.description)
			} else {
				g.Expect(err).ToNot(HaveOccurred(), test.description)
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
		"When ARO HCP data plane identities are mixed with workload identities it should return an error": {
			opts: CreateInfraOptions{
				DataPlaneIdentitiesFile: "dp.json",
				WorkloadIdentitiesFile:  "wi.json",
			},
			expectedError: true,
			description:   "Should reject mixing ARO HCP data plane identities with self-managed workload identities",
		},
		"When all deployment models are mixed together it should return an error": {
			opts: CreateInfraOptions{
				ManagedIdentitiesFile:   "mi.json",
				DataPlaneIdentitiesFile: "dp.json",
				WorkloadIdentitiesFile:  "wi.json",
			},
			expectedError: true,
			description:   "Should reject mixing all deployment models together",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			err := test.opts.validateDeploymentModelFlags()

			if test.expectedError {
				g.Expect(err).To(HaveOccurred(), test.description)
				// Verify the error mentions either mutual exclusion or missing configuration
				g.Expect(err.Error()).To(SatisfyAny(
					ContainSubstring("mutually exclusive"),
					ContainSubstring("--workload-identities-file is required"),
				), "Error should mention validation failure: %s", test.description)
			} else {
				g.Expect(err).ToNot(HaveOccurred(), test.description)
			}
		})
	}
}
