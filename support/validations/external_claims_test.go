package validations

import (
	"testing"

	. "github.com/onsi/gomega"

	configv1 "github.com/openshift/api/config/v1"
)

func TestValidateAuthenticationSpecExternalClaims(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mutate func(*configv1.ExternalClaimsSource)
		error  string
	}{
		{name: "When an anonymous source is valid it should accept it"},
		{name: "When using a request token it should accept it", mutate: func(s *configv1.ExternalClaimsSource) {
			s.Authentication.Type = configv1.ExternalSourceAuthenticationTypeRequestProvidedToken
		}},
		{name: "When credentials reference a secret it should validate settings without fetching the secret", mutate: func(s *configv1.ExternalClaimsSource) {
			s.Authentication = configv1.ExternalSourceAuthentication{Type: configv1.ExternalSourceAuthenticationTypeClientCredential, ClientCredential: configv1.ClientCredentialConfig{
				ClientID: "client", TokenEndpoint: "https://issuer.example.com/token",
				ClientSecret: configv1.ClientSecretSecretReference{Name: "credentials"}, Scopes: []configv1.OAuth2Scope{"read"},
			}}
		}},
		{name: "When a hostname includes a scheme it should reject it", mutate: func(s *configv1.ExternalClaimsSource) { s.URL.Hostname = "https://claims.example.com" }, error: "url.hostname"},
		{name: "When a hostname has an invalid port it should reject it", mutate: func(s *configv1.ExternalClaimsSource) { s.URL.Hostname = "claims.example.com:65536" }, error: "url.hostname"},
		{name: "When a hostname has a valid port it should accept it", mutate: func(s *configv1.ExternalClaimsSource) { s.URL.Hostname = "claims.example.com:8443" }},
		{name: "When a path expression has invalid syntax it should reject it", mutate: func(s *configv1.ExternalClaimsSource) { s.URL.PathExpression = "claims." }, error: "url.pathExpression"},
		{name: "When a path expression uses response it should reject it", mutate: func(s *configv1.ExternalClaimsSource) { s.URL.PathExpression = "response.path" }, error: "undeclared reference to 'response'"},
		{name: "When a mapping uses claims it should reject it", mutate: func(s *configv1.ExternalClaimsSource) { s.Mappings[0].Expression = "claims.groups" }, error: "undeclared reference to 'claims'"},
		{name: "When a mapping has an empty expression it should reject it", mutate: func(s *configv1.ExternalClaimsSource) { s.Mappings[0].Expression = "" }, error: "mappings[0].expression"},
		{name: "When a mapping name is invalid it should reject it", mutate: func(s *configv1.ExternalClaimsSource) { s.Mappings[0].Name = "group1" }, error: "mappings[0].name"},
		{name: "When mappings are empty it should reject them", mutate: func(s *configv1.ExternalClaimsSource) { s.Mappings = nil }, error: "mappings"},
		{name: "When a predicate is not boolean it should reject it", mutate: func(s *configv1.ExternalClaimsSource) { s.Predicates[0].Expression = "'yes'" }, error: "must evaluate to bool"},
		{name: "When predicates are duplicated it should reject them", mutate: func(s *configv1.ExternalClaimsSource) { s.Predicates = append(s.Predicates, s.Predicates[0]) }, error: "predicates[1].expression: Duplicate"},
		{name: "When authentication type is unknown it should reject it", mutate: func(s *configv1.ExternalClaimsSource) { s.Authentication.Type = "unknown" }, error: "authentication.type"},
		{name: "When request token authentication also has credentials it should reject it", mutate: func(s *configv1.ExternalClaimsSource) {
			s.Authentication.Type = configv1.ExternalSourceAuthenticationTypeRequestProvidedToken
			s.Authentication.ClientCredential.ClientID = "client"
		}, error: "clientCredential: Forbidden"},
		{name: "When client credentials are absent it should reject it", mutate: func(s *configv1.ExternalClaimsSource) {
			s.Authentication.Type = configv1.ExternalSourceAuthenticationTypeClientCredential
		}, error: "clientSecret.name"},
		{name: "When a token endpoint uses HTTP it should reject it", mutate: func(s *configv1.ExternalClaimsSource) {
			s.Authentication.Type = configv1.ExternalSourceAuthenticationTypeClientCredential
			s.Authentication.ClientCredential.TokenEndpoint = "http://issuer.example.com/token"
		}, error: "tokenEndpoint"},
		{name: "When a scope contains spaces it should reject it", mutate: func(s *configv1.ExternalClaimsSource) {
			s.Authentication.Type = configv1.ExternalSourceAuthenticationTypeClientCredential
			s.Authentication.ClientCredential.Scopes = []configv1.OAuth2Scope{"read write"}
		}, error: "scopes[0]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			source := configv1.ExternalClaimsSource{
				URL:        configv1.SourceURL{Hostname: "claims.example.com", PathExpression: "'/users/' + claims.sub"},
				Mappings:   []configv1.SourcedClaimMapping{{Name: "groups", Expression: "response.groups"}},
				Predicates: []configv1.ExternalSourcePredicate{{Expression: "claims.sub != ''"}},
			}
			if tc.mutate != nil {
				tc.mutate(&source)
			}
			authn := &configv1.AuthenticationSpec{Type: configv1.AuthenticationTypeOIDC, OIDCProviders: []configv1.OIDCProvider{{
				Name:                  "issuer",
				Issuer:                configv1.TokenIssuer{URL: "https://issuer.example.com", Audiences: []configv1.TokenAudience{"client"}},
				ClaimMappings:         configv1.TokenClaimMappings{Username: configv1.UsernameClaimMapping{Claim: "sub", PrefixPolicy: configv1.NoPrefix}},
				ExternalClaimsSources: []configv1.ExternalClaimsSource{source},
			}}}
			// A nil client also verifies this validation needs no referenced resources.
			err := ValidateAuthenticationSpec(t.Context(), nil, authn, "clusters", nil)
			if tc.error != "" {
				g.Expect(err).To(MatchError(ContainSubstring(tc.error)))
				g.Expect(err.Error()).To(ContainSubstring("oidcProviders[0].externalClaimsSources[0]"))
			} else {
				g.Expect(err).NotTo(HaveOccurred())
			}
		})
	}
}

func TestValidateExternalClaimsSourceLists(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		mutate func(*configv1.OIDCProvider)
		error  string
	}{
		{name: "When mappings share a name across sources it should reject them", mutate: func(p *configv1.OIDCProvider) {
			p.ExternalClaimsSources = append(p.ExternalClaimsSources, p.ExternalClaimsSources[0])
		}, error: "externalClaimsSources[1].mappings[0].name: Duplicate"},
		{name: "When there are too many sources it should reject them", mutate: func(p *configv1.OIDCProvider) { p.ExternalClaimsSources = make([]configv1.ExternalClaimsSource, 6) }, error: "externalClaimsSources: Too many"},
		{name: "When there are too many mappings it should reject them", mutate: func(p *configv1.OIDCProvider) {
			p.ExternalClaimsSources[0].Mappings = make([]configv1.SourcedClaimMapping, 17)
		}, error: "mappings: Too many"},
		{name: "When there are too many predicates it should reject them", mutate: func(p *configv1.OIDCProvider) {
			p.ExternalClaimsSources[0].Predicates = make([]configv1.ExternalSourcePredicate, 17)
		}, error: "predicates: Too many"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			provider := configv1.OIDCProvider{ExternalClaimsSources: []configv1.ExternalClaimsSource{{Mappings: []configv1.SourcedClaimMapping{{Name: "groups"}}}}}
			tc.mutate(&provider)
			authn := &configv1.AuthenticationSpec{Type: configv1.AuthenticationTypeOIDC, OIDCProviders: []configv1.OIDCProvider{provider}}
			g.Expect(ValidateAuthenticationSpec(t.Context(), nil, authn, "clusters", nil)).To(MatchError(ContainSubstring(tc.error)))
		})
	}
}
