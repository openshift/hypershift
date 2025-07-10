package util

import (
	configv1 "github.com/openshift/api/config/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
)

type ConfigWithExtOIDCParam struct {
	OIDCProviderName        string
	CliClientID             string
	ConsoleClientID         string
	IssuerURL               string
	GroupPrefix             string
	UserPrefix              string
	ConsoleClientSecretName string
}

func GetExtOIDCParam(cliClientID, consoleClientID, issuerURL string) *ConfigWithExtOIDCParam {
	return &ConfigWithExtOIDCParam{
		OIDCProviderName:        "microsoft-entra-id",
		CliClientID:             cliClientID,
		ConsoleClientID:         consoleClientID,
		IssuerURL:               issuerURL,
		GroupPrefix:             "oidc-groups-test:",
		UserPrefix:              "oidc-user-test:",
		ConsoleClientSecretName: "console-secret",
	}
}

func (config *ConfigWithExtOIDCParam) GetConfigWithExtOIDC() *hyperv1.ClusterConfiguration {
	return &hyperv1.ClusterConfiguration{
		Authentication: &configv1.AuthenticationSpec{
			OIDCProviders: []configv1.OIDCProvider{
				{
					Name: config.OIDCProviderName,
					Issuer: configv1.TokenIssuer{
						Audiences: []configv1.TokenAudience{
							configv1.TokenAudience(config.CliClientID),
							configv1.TokenAudience(config.ConsoleClientID),
						},
						URL: config.IssuerURL,
					},
					OIDCClients: []configv1.OIDCClientConfig{
						{
							ClientID: config.ConsoleClientID,
							ClientSecret: configv1.SecretNameReference{
								Name: config.ConsoleClientSecretName,
							},
							ComponentName:      "console",
							ComponentNamespace: "openshift-console",
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
							TokenClaimMapping: configv1.TokenClaimMapping{
								Claim: "email",
							},
							PrefixPolicy: configv1.Prefix,
							Prefix: &configv1.UsernamePrefix{
								PrefixString: config.UserPrefix,
							},
						},
						UID: &configv1.TokenClaimOrExpressionMapping{
							Expression: `"testuid-" + claims.sub + "-uidtest"`,
						},
						Extra: []configv1.ExtraMapping{
							{
								Key:             "extratest.openshift.com/bar",
								ValueExpression: `"extra-test-mark"`,
							},
							{
								Key:             "extratest.openshift.com/foo",
								ValueExpression: "claims.email",
							},
						},
					},
				},
			},
		},
	}
}
