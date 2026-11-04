package azure

import (
	"testing"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/support/config"

	"github.com/spf13/cobra"
)

// TestDisableClusterCapabilitiesFlagHelp verifies the shared --disable-cluster-capabilities help
// on its actual consumers describes the implemented ImageRegistry-only behavior and does not claim
// that every disabled capability suppresses an identity.
func TestDisableClusterCapabilitiesFlagHelp(t *testing.T) { //nolint:hypershiftlinter // verifies the shared --disable-cluster-capabilities help wired by both azure create commands (NewCreateCommand, NewCreateIAMCommand)
	t.Parallel()
	g := NewWithT(t)

	consumers := map[string]*cobra.Command{
		"create iam azure":   NewCreateIAMCommand(),
		"create infra azure": NewCreateCommand(),
	}
	for name, cmd := range consumers {
		flag := cmd.Flags().Lookup("disable-cluster-capabilities")
		g.Expect(flag).ToNot(BeNil(), "%s must expose --disable-cluster-capabilities", name)
		g.Expect(flag.Usage).To(Equal(util.DisableClusterCapabilitiesDescription), "%s must use the shared description", name)
		g.Expect(flag.Usage).To(ContainSubstring("ImageRegistry"), "%s help should name the ImageRegistry capability", name)
		g.Expect(flag.Usage).ToNot(ContainSubstring("Disabled capabilities will not have corresponding workload identities created"),
			"%s help must not claim that all disabled capabilities suppress identities", name)
	}
}

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
		"When supported disabled capabilities are provided it should pass validation": {
			opts: CreateIAMOptions{
				Name:                       "test-cluster",
				InfraID:                    "test-infra-id",
				CredentialsFile:            "/path/to/creds.json",
				ResourceGroupName:          "test-rg",
				OIDCIssuerURL:              "https://issuer.example.com",
				OutputFile:                 "/path/to/output.json",
				DisableClusterCapabilities: append([]string(nil), supportedDisabledClusterCapabilities...),
			},
			expectedError: false,
			description:   "Should accept the same disabled capabilities as cluster creation",
		},
		"When a disabled capability is misspelled it should return an error": {
			opts: CreateIAMOptions{
				Name:                       "test-cluster",
				InfraID:                    "test-infra-id",
				CredentialsFile:            "/path/to/creds.json",
				ResourceGroupName:          "test-rg",
				OIDCIssuerURL:              "https://issuer.example.com",
				OutputFile:                 "/path/to/output.json",
				DisableClusterCapabilities: []string{"ImageRegstry"},
			},
			expectedError: true,
			errorContains: "unknown disabled capability: ImageRegstry",
			description:   "Should reject misspelled disabled capabilities before Azure mutations",
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

	g.Expect(opts.Location).To(Equal(config.DefaultAzureLocation), "Should have default location")
	g.Expect(opts.Cloud).To(Equal(config.DefaultAzureCloud), "Should have default cloud")
}
