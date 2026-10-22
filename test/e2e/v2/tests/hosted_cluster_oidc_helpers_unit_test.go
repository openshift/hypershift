//go:build e2ev2

package tests

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	configv1 "github.com/openshift/api/config/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestOIDCFixtureAuthentication(t *testing.T) {
	for _, testCase := range []struct {
		name             string
		mutate           func(*hyperv1.HostedCluster)
		missingSecret    bool
		missingCA        bool
		missingFile      bool
		emptyUsers       bool
		standalone       bool
		additionalClaims bool
		wantError        bool
	}{
		{name: "When independent fixture references and resources agree, it should accept them"},
		{name: "When no fixture inputs are supplied, it should preserve standalone OIDC config checks without fixture resources", standalone: true, missingSecret: true, missingCA: true, mutate: func(cluster *hyperv1.HostedCluster) {
			cluster.Spec.Configuration.Authentication.OIDCProviders[0].OIDCClients = nil
		}},
		{name: "When configuration is absent, it should reject it", mutate: func(cluster *hyperv1.HostedCluster) { cluster.Spec.Configuration = nil }, wantError: true},
		{name: "When authentication is absent, it should reject it", mutate: func(cluster *hyperv1.HostedCluster) { cluster.Spec.Configuration.Authentication = nil }, wantError: true},
		{name: "When an expected fixture loses OIDC authentication type, it should fail rather than silently skip", mutate: func(cluster *hyperv1.HostedCluster) {
			cluster.Spec.Configuration.Authentication.Type = configv1.AuthenticationTypeIntegratedOAuth
		}, wantError: true},
		{name: "When provider is absent, it should reject it", mutate: func(cluster *hyperv1.HostedCluster) { cluster.Spec.Configuration.Authentication.OIDCProviders = nil }, wantError: true},
		{name: "When issuer is empty, it should reject it", mutate: func(cluster *hyperv1.HostedCluster) {
			cluster.Spec.Configuration.Authentication.OIDCProviders[0].Issuer.URL = ""
		}, wantError: true},
		{name: "When console reference names another existing Secret, it should reject independent-reference mismatch", mutate: func(cluster *hyperv1.HostedCluster) {
			cluster.Spec.Configuration.Authentication.OIDCProviders[0].OIDCClients[1].ClientSecret.Name = "other-secret"
		}, wantError: true},
		{name: "When CA reference names another existing ConfigMap, it should reject independent-reference mismatch", mutate: func(cluster *hyperv1.HostedCluster) {
			cluster.Spec.Configuration.Authentication.OIDCProviders[0].Issuer.CertificateAuthority.Name = "other-ca"
		}, wantError: true},
		{name: "When console client is absent, it should reject vacuous matching", mutate: func(cluster *hyperv1.HostedCluster) {
			cluster.Spec.Configuration.Authentication.OIDCProviders[0].OIDCClients = cluster.Spec.Configuration.Authentication.OIDCProviders[0].OIDCClients[:1]
		}, wantError: true},
		{name: "When CLI client is absent, it should reject incomplete fixture configuration", mutate: func(cluster *hyperv1.HostedCluster) {
			cluster.Spec.Configuration.Authentication.OIDCProviders[0].OIDCClients = cluster.Spec.Configuration.Authentication.OIDCProviders[0].OIDCClients[1:]
		}, wantError: true},
		{name: "When configured Secret is missing, it should not accept matching names alone", missingSecret: true, wantError: true},
		{name: "When configured CA is missing, it should not accept matching names alone", missingCA: true, wantError: true},
		{name: "When resources exist only in another namespace, it should reject them", mutate: func(cluster *hyperv1.HostedCluster) { cluster.Namespace = "other" }, wantError: true},
		{name: "When CA file is missing, it should reject incomplete handoff", missingFile: true, wantError: true},
		{name: "When test users are absent, it should reject incomplete handoff", emptyUsers: true, wantError: true},
		{name: "When additional mappings are enabled and present, it should accept them", additionalClaims: true},
		{name: "When enabled UID mapping is absent, it should reject it", additionalClaims: true, mutate: func(cluster *hyperv1.HostedCluster) {
			cluster.Spec.Configuration.Authentication.OIDCProviders[0].ClaimMappings.UID = nil
		}, wantError: true},
		{name: "When enabled Extra mappings are absent, it should reject them", additionalClaims: true, mutate: func(cluster *hyperv1.HostedCluster) {
			cluster.Spec.Configuration.Authentication.OIDCProviders[0].ClaimMappings.Extra = nil
		}, wantError: true},
		{name: "When additional mappings are disabled, it should retain older fixture compatibility", mutate: func(cluster *hyperv1.HostedCluster) {
			cluster.Spec.Configuration.Authentication.OIDCProviders[0].ClaimMappings = configv1.TokenClaimMappings{}
		}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			cluster := &hyperv1.HostedCluster{ObjectMeta: metav1.ObjectMeta{Namespace: "clusters"}, Spec: hyperv1.HostedClusterSpec{Configuration: &hyperv1.ClusterConfiguration{Authentication: &configv1.AuthenticationSpec{
				Type: configv1.AuthenticationTypeOIDC, OIDCProviders: []configv1.OIDCProvider{{
					Issuer:        configv1.TokenIssuer{URL: "https://issuer.example.test", CertificateAuthority: configv1.ConfigMapNameReference{Name: "oidc-ca"}},
					OIDCClients:   []configv1.OIDCClientConfig{{ClientID: "oc-cli-test"}, {ClientID: "console-test", ClientSecret: configv1.SecretNameReference{Name: "console-secret"}}},
					ClaimMappings: configv1.TokenClaimMappings{UID: &configv1.TokenClaimOrExpressionMapping{Claim: "sub"}, Extra: []configv1.ExtraMapping{{Key: "example.test/claim", ValueExpression: "claims.email"}}},
				}},
			}}}}
			if testCase.mutate != nil {
				testCase.mutate(cluster)
			}
			objects := []crclient.Object{&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "other-secret", Namespace: "clusters"}}, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "other-ca", Namespace: "clusters"}}}
			if !testCase.missingSecret {
				objects = append(objects, &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "console-secret", Namespace: "clusters"}})
			}
			if !testCase.missingCA {
				objects = append(objects, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "oidc-ca", Namespace: "clusters"}})
			}
			client := fake.NewClientBuilder().WithScheme(hyperapi.Scheme).WithObjects(objects...).Build()
			caFile := filepath.Join(t.TempDir(), "issuer-ca")
			if !testCase.missingFile {
				if err := os.WriteFile(caFile, []byte("synthetic-ca"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			users := "synthetic-user:synthetic-password"
			if testCase.emptyUsers {
				users = " "
			}
			if testCase.standalone {
				caFile, users = "", ""
			}
			if err := oidcFixtureAuthentication(t.Context(), client, cluster, caFile, users, testCase.additionalClaims); (err != nil) != testCase.wantError {
				t.Fatalf("unexpected fixture validation: %v", err)
			}
		})
	}
	t.Run("When canceled, it should preserve cancellation before any client access", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if err := oidcFixtureAuthentication(ctx, nil, nil, "", "", false); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation lost: %v", err)
		}
	})
}

func TestOIDCSubjectReview(t *testing.T) {
	for _, status := range []int{http.StatusCreated, http.StatusUnauthorized, http.StatusForbidden} {
		t.Run("When subject review returns "+http.StatusText(status)+", it should preserve fresh identity and status semantics", func(t *testing.T) {
			tokens, reviews := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				response.Header().Set("Content-Type", "application/json")
				if strings.HasSuffix(request.URL.Path, "/token") {
					tokens++
					_, _ = response.Write([]byte(`{"access_token":"synthetic-access","id_token":"synthetic-identity"}`))
					return
				}
				reviews++
				if request.URL.Path != "/apis/authentication.k8s.io/v1/selfsubjectreviews" || request.Header.Get("Authorization") != "Bearer synthetic-identity" {
					t.Error("SSR must use only the freshly issued identity token")
				}
				response.WriteHeader(status)
				if status == http.StatusCreated {
					_, _ = response.Write([]byte(`{"apiVersion":"authentication.k8s.io/v1","kind":"SelfSubjectReview","status":{"userInfo":{"username":"oidc-user"}}}`))
					return
				}
				_, _ = response.Write([]byte(`{"apiVersion":"v1","kind":"Status","status":"Failure","code":` + fmt.Sprint(status) + `,"reason":"` + http.StatusText(status) + `"}`))
			}))
			defer server.Close()
			keycloak := &oidcKeycloak{client: server.Client(), issuer: server.URL + "/realms/master", cliClientID: "cli"}
			config := &rest.Config{Host: server.URL, BearerToken: "synthetic-original", Username: "original", Password: "synthetic-password"}
			for range 2 {
				review, userConfig, err := oidcSubjectReview(&internal.TestContext{Context: t.Context()}, keycloak, config, oidcTestUser{username: "oidc-user", password: "synthetic-password"})
				if status == http.StatusCreated {
					if err != nil || review.Status.UserInfo.Username != "oidc-user" || userConfig.BearerToken != "synthetic-identity" || userConfig.Username != "" || userConfig.Password != "" {
						t.Fatal("fresh token identity must be isolated from administrator credentials")
					}
				} else if status == http.StatusUnauthorized && !apierrors.IsUnauthorized(err) || status == http.StatusForbidden && !apierrors.IsForbidden(err) {
					t.Fatalf("authentication and authorization errors must remain distinguishable: %v", err)
				}
			}
			if tokens != 2 || reviews != 2 || config.BearerToken != "synthetic-original" {
				t.Fatal("every review must refresh its token without changing the source credentials")
			}
		})
	}
}

func TestKeycloakAdminCredentials(t *testing.T) {
	for _, testCase := range []struct {
		name       string
		containers []corev1.Container
		wantErr    bool
	}{
		{name: "When legacy literal credentials are configured, it should return the pair", containers: []corev1.Container{{Env: []corev1.EnvVar{{Name: "KEYCLOAK_ADMIN", Value: "synthetic-admin"}, {Name: "KEYCLOAK_ADMIN_PASSWORD", Value: "synthetic-password"}}}}},
		{name: "When bootstrap literal credentials are configured, it should return the pair", containers: []corev1.Container{{Env: []corev1.EnvVar{{Name: "KC_BOOTSTRAP_ADMIN_USERNAME", Value: "synthetic-admin"}, {Name: "KC_BOOTSTRAP_ADMIN_PASSWORD", Value: "synthetic-password"}}}}},
		{name: "When credentials are absent, it should fail without disclosing values", wantErr: true},
		{name: "When credentials are split across containers, it should not combine unrelated environments", containers: []corev1.Container{{Env: []corev1.EnvVar{{Name: "KEYCLOAK_ADMIN", Value: "synthetic-admin"}}}, {Env: []corev1.EnvVar{{Name: "KEYCLOAK_ADMIN_PASSWORD", Value: "synthetic-password"}}}}, wantErr: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			statefulSet := &appsv1.StatefulSet{Spec: appsv1.StatefulSetSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: testCase.containers}}}}
			username, password, err := keycloakAdminCredentials(statefulSet)
			if (err != nil) != testCase.wantErr {
				t.Fatal("unexpected credential discovery result")
			}
			if err == nil && (username != "synthetic-admin" || password != "synthetic-password") {
				t.Fatal("literal fixture credentials were not discovered")
			}
			if err != nil && strings.Contains(err.Error(), "synthetic-") {
				t.Fatal("credential discovery errors must be redacted")
			}
		})
	}
	if _, _, err := keycloakAdminCredentials(nil); err == nil {
		t.Fatal("nil StatefulSet must be rejected")
	}
}

func TestKeycloakToken(t *testing.T) {
	for _, testCase := range []struct {
		name, body string
		status     int
		wantErr    bool
	}{
		{name: "When the issuer supplies both tokens, it should return them", status: http.StatusOK, body: `{"access_token":"synthetic-access","id_token":"synthetic-id"}`},
		{name: "When the issuer rejects authentication, it should redact its response", status: http.StatusUnauthorized, body: `synthetic-secret`, wantErr: true},
		{name: "When the issuer response is malformed, it should return a redacted error", status: http.StatusOK, body: `synthetic-secret`, wantErr: true},
		{name: "When a token is absent, it should reject incomplete responses", status: http.StatusOK, body: `{"access_token":"synthetic-secret"}`, wantErr: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if err := request.ParseForm(); err != nil {
					t.Error("could not parse token form")
				}
				if request.Method != http.MethodPost || request.Form.Get("grant_type") != "password" || request.Form.Get("client_id") != "test-cli" || request.Form.Get("username") != "test-user" || request.Form.Get("password") != "synthetic-password" {
					t.Error("unexpected password-grant request")
				}
				response.WriteHeader(testCase.status)
				_, _ = response.Write([]byte(testCase.body))
			}))
			defer server.Close()
			access, identity, err := keycloakToken(t.Context(), server.Client(), server.URL, "test-cli", "test-user", "synthetic-password")
			if (err != nil) != testCase.wantErr {
				t.Fatalf("unexpected token request result: %v", err)
			}
			if err != nil && strings.Contains(err.Error(), "synthetic-secret") {
				t.Fatal("token errors must not expose response bodies")
			}
			if !testCase.wantErr && (access != "synthetic-access" || identity != "synthetic-id") {
				t.Fatal("both tokens must be returned")
			}
		})
	}
	t.Run("When the context is canceled, it should stop the token request", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if _, _, err := keycloakToken(ctx, http.DefaultClient, "https://keycloak.example.test/realms/master", "cli", "user", "synthetic-password"); err == nil {
			t.Fatal("canceled token request must fail")
		}
	})
}

func TestOIDCKeycloakAdminRequest(t *testing.T) {
	for _, testCase := range []struct {
		name, method, location string
		status                 int
		wantErr                bool
	}{
		{name: "When a user is created, it should return its resource identifier", method: http.MethodPost, location: "/admin/realms/master/users/test-id", status: http.StatusCreated},
		{name: "When a creation omits its resource location, it should fail", method: http.MethodPost, status: http.StatusCreated, wantErr: true},
		{name: "When a creation identifies the collection rather than a resource, it should fail", method: http.MethodPost, location: "/admin/realms/master/users/", status: http.StatusCreated, wantErr: true},
		{name: "When a creation identifies an unrelated collection, it should fail", method: http.MethodPost, location: "/admin/realms/master/groups/test-id", status: http.StatusCreated, wantErr: true},
		{name: "When a resource is deleted, it should accept NoContent", method: http.MethodDelete, status: http.StatusNoContent},
		{name: "When a resource was already removed, it should accept NotFound", method: http.MethodDelete, status: http.StatusNotFound},
		{name: "When admin access is denied, it should report a redacted error", method: http.MethodDelete, status: http.StatusForbidden, wantErr: true},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
				if strings.HasSuffix(request.URL.Path, "/token") {
					_, _ = response.Write([]byte(`{"access_token":"synthetic-access","id_token":"synthetic-id"}`))
					return
				}
				if request.URL.Path != "/admin/realms/master/users" || request.Header.Get("Authorization") != "Bearer synthetic-access" {
					t.Error("unexpected admin endpoint or authorization")
				}
				if testCase.location != "" {
					response.Header().Set("Location", testCase.location)
				}
				response.WriteHeader(testCase.status)
			}))
			defer server.Close()
			keycloak := &oidcKeycloak{client: server.Client(), issuer: server.URL + "/realms/master", adminUsername: "admin", adminPassword: "synthetic-password"}
			identifier, err := keycloak.adminRequest(t.Context(), testCase.method, "users", map[string]string{"username": "test-user"})
			if (err != nil) != testCase.wantErr {
				t.Fatalf("unexpected admin request result: %v", err)
			}
			if !testCase.wantErr && testCase.method == http.MethodPost && identifier != "test-id" {
				t.Fatal("unexpected resource identifier")
			}
		})
	}
}

func TestUpstreamOIDCAuthentication(t *testing.T) {
	t.Run("When authentication is missing, it should reject the input", func(t *testing.T) {
		for _, original := range []*configv1.AuthenticationSpec{nil, {}} {
			if _, err := upstreamOIDCAuthentication(original); err == nil {
				t.Fatal("missing provider must fail")
			}
		}
	})
	t.Run("When a provider is configured, it should replace only the CEL mapping and validation fields without aliasing the original", func(t *testing.T) {
		original := &configv1.AuthenticationSpec{OIDCProviders: []configv1.OIDCProvider{{Issuer: configv1.TokenIssuer{URL: "https://keycloak.example.test/realms/master"}, ClaimMappings: configv1.TokenClaimMappings{Username: configv1.UsernameClaimMapping{Claim: "email"}}}}}
		snapshot := original.DeepCopy()
		updated, err := upstreamOIDCAuthentication(original)
		if err != nil {
			t.Fatal(err)
		}
		provider := updated.OIDCProviders[0]
		if !reflect.DeepEqual(original, snapshot) || provider.Issuer.URL != original.OIDCProviders[0].Issuer.URL {
			t.Fatal("source authentication and issuer must be preserved")
		}
		if provider.ClaimMappings.Username.Expression != "claims.email.split('@')[0]" || provider.ClaimMappings.Username.Claim != "" || provider.ClaimMappings.Groups.Expression != "claims.?groups.orValue([])" {
			t.Fatal("CEL mapping must replace static claim mapping")
		}
		if len(provider.ClaimValidationRules) != 1 || provider.ClaimValidationRules[0].CEL.Expression != "claims.email_verified == true" || len(provider.UserValidationRules) != 1 || provider.UserValidationRules[0].Expression != "!user.username.contains('forbidden')" {
			t.Fatal("both claim and user validation rules must be configured")
		}
	})
}
