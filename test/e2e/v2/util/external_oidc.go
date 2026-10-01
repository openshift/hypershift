//go:build e2ev2

/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package util

import (
	"fmt"

	"github.com/openshift/hypershift/control-plane-operator/featuregates"

	configv1 "github.com/openshift/api/config/v1"
)

// ProviderType identifies the external OIDC provider kind used in e2e tests.
type ProviderType string

const (
	ProviderAzure    ProviderType = "azure"
	ProviderKeycloak ProviderType = "keycloak"

	ExternalOIDCUIDExpressionPrefix        = "testuid-"
	ExternalOIDCUIDExpressionSubfix        = "-uidtest"
	ExternalOIDCExtraKeyBar                = "extratest.openshift.com/bar"
	ExternalOIDCExtraKeyBarValueExpression = "extra-test-mark"
	ExternalOIDCExtraKeyFoo                = "extratest.openshift.com/foo"
	ExternalOIDCExtraKeyFooValueExpression = "claims.email"
)

// ExtOIDCConfig holds the configuration for an external OIDC provider used in e2e tests.
type ExtOIDCConfig struct {
	ExternalOIDCProvider     ProviderType
	OIDCProviderName         string
	CliClientID              string
	ConsoleClientID          string
	IssuerURL                string
	GroupPrefix              string
	UserPrefix               string
	ConsoleClientSecretName  string
	ConsoleClientSecretValue string

	// format: "user1:psw1,user2:psw2", used for keycloak oidc
	TestUsers string

	// for oidcProviders.issuer.issuerCertificateAuthority
	IssuerCAConfigmapName string
	IssuerCABundleFile    string

	// CustomizeAuthSpec allows tests to modify the baseline auth configuration
	CustomizeAuthSpec func(*configv1.AuthenticationSpec)
}

// GetExtOIDCConfig constructs an ExtOIDCConfig from the given provider parameters.
func GetExtOIDCConfig(provider, cliClientID, consoleClientID, issuerURL, consoleSecret, issuerCABundleFile, testUsers string) *ExtOIDCConfig {
	return &ExtOIDCConfig{
		ExternalOIDCProvider:     ProviderType(provider),
		OIDCProviderName:         provider + " oidc server",
		CliClientID:              cliClientID,
		ConsoleClientID:          consoleClientID,
		IssuerURL:                issuerURL,
		GroupPrefix:              "oidc-groups-test:",
		UserPrefix:               "oidc-user-test:",
		ConsoleClientSecretName:  "console-secret",
		ConsoleClientSecretValue: consoleSecret,
		IssuerCAConfigmapName:    "oidc-ca",
		IssuerCABundleFile:       issuerCABundleFile,
		TestUsers:                testUsers,
	}
}

// GetAuthenticationConfig returns the AuthenticationSpec for this ExtOIDCConfig.
func (config *ExtOIDCConfig) GetAuthenticationConfig() *configv1.AuthenticationSpec {
	authnSpec := &configv1.AuthenticationSpec{
		Type: configv1.AuthenticationTypeOIDC,
		OIDCProviders: []configv1.OIDCProvider{
			{
				Name: config.OIDCProviderName,
				Issuer: configv1.TokenIssuer{
					Audiences: []configv1.TokenAudience{
						configv1.TokenAudience(config.CliClientID),
						configv1.TokenAudience(config.ConsoleClientID),
					},
					URL: config.IssuerURL,
					CertificateAuthority: configv1.ConfigMapNameReference{
						Name: config.IssuerCAConfigmapName,
					},
				},
				OIDCClients: []configv1.OIDCClientConfig{
					{
						ClientID:           config.CliClientID,
						ComponentName:      "cli",
						ComponentNamespace: "openshift-console",
						ExtraScopes:        []string{"email"},
					},
					{
						ClientID: config.ConsoleClientID,
						ClientSecret: configv1.SecretNameReference{
							Name: config.ConsoleClientSecretName,
						},
						ComponentName:      "console",
						ComponentNamespace: "openshift-console",
						ExtraScopes:        []string{"email"},
					},
				},
				ClaimMappings: configv1.TokenClaimMappings{
					Groups: configv1.PrefixedClaimMapping{
						TokenClaimMapping: configv1.TokenClaimMapping{
							Claim: "groups",
						},
						Prefix: config.GroupPrefix,
					},
					Username: configv1.UsernameClaimMapping{
						Claim:        "email",
						PrefixPolicy: configv1.Prefix,
						Prefix: &configv1.UsernamePrefix{
							PrefixString: config.UserPrefix,
						},
					},
				},
			},
		},
	}

	if featuregates.Gate().Enabled(featuregates.ExternalOIDCWithUIDAndExtraClaimMappings) {
		authnSpec.OIDCProviders[0].ClaimMappings.UID = &configv1.TokenClaimOrExpressionMapping{
			Expression: fmt.Sprintf(`"%s" + claims.sub + "%s"`, ExternalOIDCUIDExpressionPrefix, ExternalOIDCUIDExpressionSubfix),
		}

		authnSpec.OIDCProviders[0].ClaimMappings.Extra = append(authnSpec.OIDCProviders[0].ClaimMappings.Extra,
			configv1.ExtraMapping{
				Key:             ExternalOIDCExtraKeyBar,
				ValueExpression: fmt.Sprintf(`"%s"`, ExternalOIDCExtraKeyBarValueExpression),
			},
			configv1.ExtraMapping{
				Key:             ExternalOIDCExtraKeyFoo,
				ValueExpression: ExternalOIDCExtraKeyFooValueExpression,
			},
		)
	}

	if config.CustomizeAuthSpec != nil {
		config.CustomizeAuthSpec(authnSpec)
	}

	return authnSpec
}
