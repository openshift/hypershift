//go:build e2ev2

package tests

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/test/e2e/v2/internal"

	configv1 "github.com/openshift/api/config/v1"

	appsv1 "k8s.io/api/apps/v1"
	authenticationv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilrand "k8s.io/apimachinery/pkg/util/rand"
	authenticationclient "k8s.io/client-go/kubernetes/typed/authentication/v1"
	"k8s.io/client-go/rest"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type oidcKeycloak struct {
	client                                            *http.Client
	issuer, cliClientID, adminUsername, adminPassword string
}

type oidcTestUser struct {
	username, email, password, group, subject string
}

func oidcFixtureAuthentication(ctx context.Context, client crclient.Client, cluster *hyperv1.HostedCluster, caFile, testUsers string, additionalClaims bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if caFile == "" && testUsers == "" {
		return nil
	}
	if cluster == nil || cluster.Namespace == "" || cluster.Spec.Configuration == nil || cluster.Spec.Configuration.Authentication == nil {
		return fmt.Errorf("external OIDC fixture requires a namespaced hosted cluster with authentication configuration")
	}
	authentication := cluster.Spec.Configuration.Authentication
	if authentication.Type != configv1.AuthenticationTypeOIDC || len(authentication.OIDCProviders) == 0 {
		return fmt.Errorf("external OIDC fixture requires an OIDC provider")
	}
	if caFile == "" || strings.TrimSpace(testUsers) == "" {
		return fmt.Errorf("external OIDC fixture requires CA bundle file and test-user inputs")
	}
	if _, err := os.Stat(caFile); err != nil {
		return fmt.Errorf("external OIDC issuer CA bundle file is unavailable: %w", err)
	}
	provider := authentication.OIDCProviders[0]
	if provider.Issuer.URL == "" {
		return fmt.Errorf("external OIDC fixture requires an issuer URL")
	}
	if additionalClaims && (provider.ClaimMappings.UID == nil || len(provider.ClaimMappings.Extra) == 0) {
		return fmt.Errorf("external OIDC fixture requires enabled UID and Extra claim mappings")
	}
	consoleSecret := &corev1.Secret{}
	if err := client.Get(ctx, crclient.ObjectKey{Namespace: cluster.Namespace, Name: "console-secret"}, consoleSecret); err != nil {
		return fmt.Errorf("read external OIDC console client Secret: %w", err)
	}
	cliFound, consoleFound := false, false
	for _, oidcClient := range provider.OIDCClients {
		switch oidcClient.ClientID {
		case "oc-cli-test":
			cliFound = true
		case "console-test":
			consoleFound = true
			if oidcClient.ClientSecret.Name != consoleSecret.Name {
				return fmt.Errorf("external OIDC console client must reference the configured console Secret")
			}
		}
	}
	if !cliFound || !consoleFound {
		return fmt.Errorf("external OIDC fixture requires its configured CLI and console clients")
	}
	issuerCA := &corev1.ConfigMap{}
	if err := client.Get(ctx, crclient.ObjectKey{Namespace: cluster.Namespace, Name: "oidc-ca"}, issuerCA); err != nil {
		return fmt.Errorf("read external OIDC issuer CA ConfigMap: %w", err)
	}
	if provider.Issuer.CertificateAuthority.Name != issuerCA.Name {
		return fmt.Errorf("external OIDC issuer must reference the configured CA ConfigMap")
	}
	return nil
}

func keycloakForHostedCluster(tc *internal.TestContext, hc *hyperv1.HostedCluster) *oidcKeycloak {
	provider := hc.Spec.Configuration.Authentication.OIDCProviders[0]
	caPath := internal.GetEnvVarValue("E2E_EXTERNAL_OIDC_CA_BUNDLE_FILE")
	Expect(caPath).NotTo(BeEmpty(), "OIDC fixture must provide its issuer CA bundle")
	ca, err := os.ReadFile(caPath)
	Expect(err).NotTo(HaveOccurred())
	roots := x509.NewCertPool()
	Expect(roots.AppendCertsFromPEM(ca)).To(BeTrue(), "OIDC issuer CA bundle must contain a certificate")
	keycloak := &oidcKeycloak{issuer: provider.Issuer.URL,
		client: &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}}}
	for _, oidcClient := range provider.OIDCClients {
		if oidcClient.ComponentName == "cli" {
			keycloak.cliClientID = oidcClient.ClientID
		}
	}
	Expect(keycloak.cliClientID).NotTo(BeEmpty())
	statefulSet := &appsv1.StatefulSet{}
	Expect(tc.MgmtClient.Get(tc.Context, crclient.ObjectKey{Namespace: "keycloak", Name: "keycloak"}, statefulSet)).To(Succeed())
	keycloak.adminUsername, keycloak.adminPassword, err = keycloakAdminCredentials(statefulSet)
	Expect(err).NotTo(HaveOccurred())
	return keycloak
}

func keycloakAdminCredentials(statefulSet *appsv1.StatefulSet) (string, string, error) {
	if statefulSet == nil {
		return "", "", fmt.Errorf("Keycloak fixture must provide its StatefulSet")
	}
	for _, container := range statefulSet.Spec.Template.Spec.Containers {
		var username, password string
		for _, variable := range container.Env {
			switch variable.Name {
			case "KEYCLOAK_ADMIN", "KC_BOOTSTRAP_ADMIN_USERNAME":
				username = variable.Value
			case "KEYCLOAK_ADMIN_PASSWORD", "KC_BOOTSTRAP_ADMIN_PASSWORD":
				password = variable.Value
			}
		}
		if username != "" && password != "" {
			return username, password, nil
		}
	}
	return "", "", fmt.Errorf("Keycloak fixture must expose literal admin credentials through the same StatefulSet container environment")
}

func keycloakToken(ctx context.Context, client *http.Client, issuer, clientID, username, password string) (string, string, error) {
	form := url.Values{"grant_type": {"password"}, "client_id": {clientID}, "username": {username}, "password": {password}, "scope": {"openid email profile"}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, issuer+"/protocol/openid-connect/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", "", fmt.Errorf("invalid Keycloak token endpoint")
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := client.Do(request)
	if err != nil {
		return "", "", fmt.Errorf("Keycloak token request failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("Keycloak token request returned HTTP %d", response.StatusCode)
	}
	var tokens struct {
		AccessToken string `json:"access_token"`
		IDToken     string `json:"id_token"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&tokens); err != nil {
		return "", "", fmt.Errorf("Keycloak token response is invalid")
	}
	if tokens.AccessToken == "" || tokens.IDToken == "" {
		return "", "", fmt.Errorf("Keycloak token response is incomplete")
	}
	return tokens.AccessToken, tokens.IDToken, nil
}

func (keycloak *oidcKeycloak) adminRequest(ctx context.Context, method, resource string, value any) (string, error) {
	accessToken, _, err := keycloakToken(ctx, keycloak.client, keycloak.issuer, "admin-cli", keycloak.adminUsername, keycloak.adminPassword)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("cannot encode Keycloak admin request")
	}
	adminURL := strings.Replace(keycloak.issuer, "/realms/", "/admin/realms/", 1) + "/" + resource
	request, err := http.NewRequestWithContext(ctx, method, adminURL, bytes.NewReader(payload))
	if err != nil {
		return "", fmt.Errorf("invalid Keycloak admin endpoint")
	}
	request.Header.Set("Authorization", "Bearer "+accessToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := keycloak.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("Keycloak admin request failed")
	}
	defer response.Body.Close()
	if method == http.MethodDelete && response.StatusCode == http.StatusNotFound {
		return "", nil
	}
	if response.StatusCode != http.StatusCreated && response.StatusCode != http.StatusNoContent {
		return "", fmt.Errorf("Keycloak admin request returned HTTP %d", response.StatusCode)
	}
	if method == http.MethodPost {
		location, err := response.Location()
		if err != nil {
			return "", fmt.Errorf("Keycloak create response did not identify its resource")
		}
		expectedPath := strings.TrimSuffix(request.URL.Path, "/") + "/"
		identifier := strings.TrimPrefix(location.Path, expectedPath)
		if !strings.HasPrefix(location.Path, expectedPath) || identifier == "" || strings.Contains(identifier, "/") {
			return "", fmt.Errorf("Keycloak create response did not identify its requested resource")
		}
		return identifier, nil
	}
	return "", nil
}

func createOIDCTestUser(tc *internal.TestContext, keycloak *oidcKeycloak, prefix string, verified bool) oidcTestUser {
	user := oidcTestUser{username: prefix + "oidc-e2e-" + utilrand.String(10), password: utilrand.String(32), group: "oidc-e2e-" + utilrand.String(12)}
	user.email = user.username + "@test.example.com"
	groupID, err := keycloak.adminRequest(tc.Context, http.MethodPost, "groups", map[string]any{"name": user.group})
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(tc.Context), time.Minute)
		defer cancel()
		_, err := keycloak.adminRequest(cleanupContext, http.MethodDelete, "groups/"+url.PathEscape(groupID), nil)
		Expect(err).NotTo(HaveOccurred())
	})
	userID, err := keycloak.adminRequest(tc.Context, http.MethodPost, "users", map[string]any{
		"username": user.username, "email": user.email, "enabled": true, "emailVerified": verified, "firstName": user.username, "lastName": "Test",
		"groups": []string{user.group}, "credentials": []map[string]any{{"type": "password", "value": user.password, "temporary": false}},
	})
	Expect(err).NotTo(HaveOccurred())
	user.subject = userID
	DeferCleanup(func() {
		cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(tc.Context), time.Minute)
		defer cancel()
		_, err := keycloak.adminRequest(cleanupContext, http.MethodDelete, "users/"+url.PathEscape(userID), nil)
		Expect(err).NotTo(HaveOccurred())
	})
	return user
}

func oidcSubjectReview(tc *internal.TestContext, keycloak *oidcKeycloak, config *rest.Config, user oidcTestUser) (*authenticationv1.SelfSubjectReview, *rest.Config, error) {
	_, idToken, err := keycloakToken(tc.Context, keycloak.client, keycloak.issuer, keycloak.cliClientID, user.username, user.password)
	if err != nil {
		return nil, nil, err
	}
	userConfig := rest.AnonymousClientConfig(config)
	userConfig.BearerToken = idToken
	userConfig.Timeout = 10 * time.Second
	client, err := authenticationclient.NewForConfig(userConfig)
	if err != nil {
		return nil, nil, err
	}
	review, err := client.SelfSubjectReviews().Create(tc.Context, &authenticationv1.SelfSubjectReview{}, metav1.CreateOptions{})
	return review, userConfig, err
}

func requireOIDCFeature(tc *internal.TestContext, hc *hyperv1.HostedCluster, name string) {
	client, err := tc.GetHostedClusterClient(hc)
	Expect(err).NotTo(HaveOccurred())
	gate := &configv1.FeatureGate{}
	Expect(client.Get(tc.Context, crclient.ObjectKey{Name: "cluster"}, gate)).To(Succeed())
	Expect(hc.Status.Version).NotTo(BeNil())
	Expect(hc.Status.Version.History).NotTo(BeEmpty())
	for _, details := range gate.Status.FeatureGates {
		if details.Version != hc.Status.Version.History[0].Version {
			continue
		}
		for _, enabled := range details.Enabled {
			if string(enabled.Name) == name {
				return
			}
		}
		Skip("OIDC scenario requires feature gate " + name)
	}
	Fail("hosted cluster FeatureGate status does not report the running release")
}
