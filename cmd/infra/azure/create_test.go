package azure

import (
	"testing"

	. "github.com/onsi/gomega"
)

func TestValidateDeploymentModelFlags(t *testing.T) {
	tests := map[string]struct {
		opts          CreateInfraOptions
		expectedError bool
		description   string
	}{
		"valid ARO HCP with both managed and data plane identities": {
			opts: CreateInfraOptions{
				ManagedIdentitiesFile:    "mi.json",
				DataPlaneIdentitiesFile:  "dp.json",
			},
			expectedError: false,
			description:   "Should allow both ARO HCP identity files together",
		},
		"valid self-managed with workload identities file": {
			opts: CreateInfraOptions{
				WorkloadIdentitiesFile: "wi.json",
			},
			expectedError: false,
			description:   "Should allow workload identities file alone",
		},
		"valid self-managed with OIDC issuer URL": {
			opts: CreateInfraOptions{
				OIDCIssuerURL: "https://issuer.com",
			},
			expectedError: false,
			description:   "Should allow OIDC issuer URL alone",
		},
		
		// Invalid empty configuration
		"invalid empty configuration": {
			opts:          CreateInfraOptions{},
			expectedError: true,
			description:   "Should reject empty configuration without any identity settings",
		},

		// Invalid cross-deployment model cases  
		"invalid ARO HCP managed identities with self-managed workload identities": {
			opts: CreateInfraOptions{
				ManagedIdentitiesFile:  "mi.json",
				WorkloadIdentitiesFile: "wi.json",
			},
			expectedError: true,
			description:   "Should reject mixing ARO HCP managed identities with self-managed workload identities",
		},
		"valid ARO HCP managed identities with OIDC issuer": {
			opts: CreateInfraOptions{
				ManagedIdentitiesFile: "mi.json",
				OIDCIssuerURL:         "https://issuer.com",
			},
			expectedError: false,
			description:   "Should allow ARO HCP managed identities with OIDC issuer",
		},
		"invalid ARO HCP data plane identities with self-managed workload identities": {
			opts: CreateInfraOptions{
				DataPlaneIdentitiesFile: "dp.json",
				WorkloadIdentitiesFile:  "wi.json",
			},
			expectedError: true,
			description:   "Should reject mixing ARO HCP data plane identities with self-managed workload identities",
		},
		"valid ARO HCP data plane identities with OIDC issuer": {
			opts: CreateInfraOptions{
				DataPlaneIdentitiesFile: "dp.json",
				OIDCIssuerURL:           "https://issuer.com",
			},
			expectedError: false,
			description:   "Should allow ARO HCP data plane identities with OIDC issuer",
		},

		// Invalid self-managed internal conflicts
		"invalid self-managed workload identities with OIDC issuer": {
			opts: CreateInfraOptions{
				WorkloadIdentitiesFile: "wi.json",
				OIDCIssuerURL:          "https://issuer.com",
			},
			expectedError: true,
			description:   "Should reject mixing self-managed workload identities file with OIDC issuer URL",
		},

		// Complex invalid combinations
		"invalid all deployment models mixed": {
			opts: CreateInfraOptions{
				ManagedIdentitiesFile:   "mi.json",
				DataPlaneIdentitiesFile: "dp.json",
				WorkloadIdentitiesFile:  "wi.json",
				OIDCIssuerURL:           "https://issuer.com",
			},
			expectedError: true,
			description:   "Should reject mixing all deployment models together",
		},
		"invalid triple conflict - managed, workload, oidc": {
			opts: CreateInfraOptions{
				ManagedIdentitiesFile:  "mi.json",
				WorkloadIdentitiesFile: "wi.json",
				OIDCIssuerURL:          "https://issuer.com",
			},
			expectedError: true,
			description:   "Should reject triple conflict between managed identities, workload identities, and OIDC issuer",
		},
		"invalid triple conflict - data plane, workload, oidc": {
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