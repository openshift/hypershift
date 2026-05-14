package util

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/featuregates"

	configv1 "github.com/openshift/api/config/v1"
	configv1typedclient "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"

	appsv1 "k8s.io/api/apps/v1"
	kauthnv1 "k8s.io/api/authentication/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kauthnv1typedclient "k8s.io/client-go/kubernetes/typed/authentication/v1"
	"k8s.io/client-go/rest"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type ProviderType string

const (
	ProviderAzure    ProviderType = "azure"
	ProviderKeycloak ProviderType = "keycloak"

	ExternalOIDCUIDExpressionPrefix        = "testuid-"
	ExternalOIDCUIDExpressionSubfix        = "-uidtest"
	ExternalOIDCExtraKeyBar                = "extratest.openshift.com/bar"
	ExternalOIDCExtraKeyBarValueExpression = "extra-test-mark"
	ExternalOIDCExtraKeyFoo                = "extratest.openshift.com/foo"
	ExternalOIDCExtraKeyFooValueExpression = "claims.email" // This is a variable, not a string literal

	// CEL expressions for claim validation rules
	ClaimValidationExprEmailExists   = "has(claims.email) && claims.email != ''"
	ClaimValidationExprEmailVerified = "claims.email_verified == true"

	// CEL expressions for user validation rules
	UserValidationExprNoSystemPrefix  = "!user.username.startsWith('system:')"
	UserValidationExprNoForbiddenWord = "!user.username.contains('forbidden')"
)

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

	// format: “user1:psw1,user2:psw2”, it is used for keycloak oidc
	TestUsers string

	// for oidcProviders.issuer.issuerCertificateAuthority
	IssuerCAConfigmapName string
	IssuerCABundleFile    string
}

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

	if featuregates.Gate().Enabled(featuregates.ExternalOIDCWithUpstreamParity) {
		// Check if ExternalOIDCWithUIDAndExtraClaimMappings feature gate is enabled.
		// If not, we will need to add extra mapping to access email for username
		// verification later.
		if !featuregates.Gate().Enabled(featuregates.ExternalOIDCWithUIDAndExtraClaimMappings) {
			authnSpec.OIDCProviders[0].ClaimMappings.Extra = append(authnSpec.OIDCProviders[0].ClaimMappings.Extra,
				configv1.ExtraMapping{
					Key:             ExternalOIDCExtraKeyFoo,
					ValueExpression: ExternalOIDCExtraKeyFooValueExpression,
				},
			)
		}
		// Use CEL expression for username mapping instead of static claim
		authnSpec.OIDCProviders[0].ClaimMappings.Username = configv1.UsernameClaimMapping{
			Expression: "claims.email.split('@')[0]",
		}

		// Use CEL expression for groups mapping instead of static claim
		authnSpec.OIDCProviders[0].ClaimMappings.Groups = configv1.PrefixedClaimMapping{
			TokenClaimMapping: configv1.TokenClaimMapping{
				Expression: "claims.?groups.orValue([])",
			},
		}

		// Add claim validation rules
		authnSpec.OIDCProviders[0].ClaimValidationRules = []configv1.TokenClaimValidationRule{
			{
				Type: configv1.TokenValidationRuleTypeCEL,
				CEL: configv1.TokenClaimValidationCELRule{
					Expression: ClaimValidationExprEmailExists,
					Message:    "email claim must be present and non-empty",
				},
			},
			{
				Type: configv1.TokenValidationRuleTypeCEL,
				CEL: configv1.TokenClaimValidationCELRule{
					Expression: ClaimValidationExprEmailVerified,
					Message:    "email_verified claim must be true",
				},
			},
		}

		// Add user validation rules
		authnSpec.OIDCProviders[0].UserValidationRules = []configv1.TokenUserValidationRule{
			{
				Expression: UserValidationExprNoSystemPrefix,
				Message:    "username cannot use reserved system: prefix",
			},
			{
				Expression: UserValidationExprNoForbiddenWord,
				Message:    "username cannot contain the word 'forbidden'",
			},
		}
	}

	return authnSpec
}

// ValidateAuthenticationSpec validates the external OIDC configuration and the expected HostedCluster authentication configuration before running the test
func ValidateAuthenticationSpec(t testing.TB, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster, config *ExtOIDCConfig) {
	g := NewWithT(t)

	// check auth config
	g.Expect(config).NotTo(BeNil())
	g.Expect(config.IssuerURL).NotTo(BeEmpty())
	g.Expect(config.CliClientID).NotTo(BeEmpty())
	g.Expect(config.ConsoleClientID).NotTo(BeEmpty())
	g.Expect(config.TestUsers).NotTo(BeEmpty())
	g.Expect(config.ConsoleClientSecretName).NotTo(BeEmpty())
	g.Expect(config.IssuerCABundleFile).NotTo(BeEmpty())
	_, err := os.Stat(config.IssuerCABundleFile)
	g.Expect(err).NotTo(HaveOccurred())

	// check hosted cluster auth configuration
	g.Expect(hostedCluster.Spec.Configuration).NotTo(BeNil())
	g.Expect(hostedCluster.Spec.Configuration.Authentication).NotTo(BeNil())
	actualAuth := hostedCluster.Spec.Configuration.Authentication
	g.Expect(actualAuth.OIDCProviders).NotTo(BeEmpty())

	if featuregates.Gate().Enabled(featuregates.ExternalOIDCWithUpstreamParity) || featuregates.Gate().Enabled(featuregates.ExternalOIDCWithUIDAndExtraClaimMappings) {
		g.Expect(actualAuth.OIDCProviders[0].ClaimMappings.Extra).NotTo(BeEmpty())
	}

	if featuregates.Gate().Enabled(featuregates.ExternalOIDCWithUIDAndExtraClaimMappings) {
		g.Expect(actualAuth.OIDCProviders[0].ClaimMappings.UID).NotTo(BeNil())
	}

	secret := &corev1.Secret{}
	err = client.Get(ctx, crclient.ObjectKey{
		Name:      config.ConsoleClientSecretName,
		Namespace: hostedCluster.Namespace,
	}, secret)
	g.Expect(err).NotTo(HaveOccurred())
	for _, client := range actualAuth.OIDCProviders[0].OIDCClients {
		if client.ClientID == config.ConsoleClientID {
			g.Expect(client.ClientSecret.Name).Should(Equal(secret.Name))
		}
	}

	cm := &corev1.ConfigMap{}
	err = client.Get(ctx, crclient.ObjectKey{
		Name:      config.IssuerCAConfigmapName,
		Namespace: hostedCluster.Namespace,
	}, cm)
	g.Expect(err).NotTo(HaveOccurred())
	g.Expect(actualAuth.OIDCProviders[0].Issuer.CertificateAuthority.Name).Should(Equal(cm.Name))
}

// IsExternalOIDCCluster checks if the cluster is using external OIDC.
func IsExternalOIDCCluster(t testing.TB, ctx context.Context, clientCfg *rest.Config) (bool, error) {
	configv1Client, err := configv1typedclient.NewForConfig(clientCfg)
	if err != nil {
		return false, err
	}
	authConfig, err := configv1Client.Authentications().Get(ctx, "cluster", metav1.GetOptions{})
	if err != nil {
		return false, err
	}
	t.Logf("Found authentication type used: %v", authConfig.Spec.Type)
	return authConfig.Spec.Type == configv1.AuthenticationTypeOIDC, nil
}

// ChangeClientForKeycloakExtOIDC changes the guest client using a keycloak user config
func ChangeClientForKeycloakExtOIDC(t testing.TB, ctx context.Context, clientCfg *rest.Config, authConfig *ExtOIDCConfig) crclient.Client {
	g := NewWithT(t)
	newConfig := ChangeUserForKeycloakExtOIDC(t, ctx, clientCfg, authConfig)
	client, err := crclient.New(newConfig, crclient.Options{Scheme: scheme})
	g.Expect(err).NotTo(HaveOccurred(), "could not create guest client using the new config")
	return client
}

// ChangeUserForKeycloakExtOIDC changes the user of current CLI session for a Keycloak external OIDC cluster
func ChangeUserForKeycloakExtOIDC(t testing.TB, ctx context.Context, clientCfg *rest.Config, authConfig *ExtOIDCConfig) *rest.Config {
	g := NewWithT(t)
	g.Expect(authConfig).NotTo(BeNil())
	g.Expect(authConfig.ExternalOIDCProvider).Should(Equal(ProviderKeycloak))
	isExternalOIDCCluster, err := IsExternalOIDCCluster(t, ctx, clientCfg)
	g.Expect(err).NotTo(HaveOccurred(), "Failed to check if the cluster's authentication config is OIDC")
	g.Expect(isExternalOIDCCluster).To(BeTrue(), "The cluster's authentication config is not OIDC")

	// KEYCLOAK_TEST_USERS has format like "user1:password1,user2:password2,...,usern:passwordn" and n (i.e. 50) is enough for parallel running cases
	re := regexp.MustCompile(`([^:,]+):([^,]+)`)
	testUsers := re.FindAllStringSubmatch(authConfig.TestUsers, -1)
	usersTotal := len(testUsers)
	var username, password string

	// Pick a random user for current running case to use
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	userIndex := r.Intn(usersTotal)
	username = testUsers[userIndex][1]
	g.Expect(username).NotTo(BeEmpty())
	password = testUsers[userIndex][2]
	g.Expect(password).NotTo(BeEmpty())
	t.Logf("Random test user for use: '%s'.", username)

	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
	}
	requestURL := authConfig.IssuerURL + "/protocol/openid-connect/token"
	oidcClientID := authConfig.CliClientID
	g.Expect(oidcClientID).NotTo(BeEmpty())
	formData := url.Values{
		"client_id":  []string{oidcClientID},
		"grant_type": []string{"password"},
		"password":   []string{password},
		"scope":      []string{"openid email profile"},
		"username":   []string{username},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, strings.NewReader(formData.Encode()))
	g.Expect(err).NotTo(HaveOccurred())
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := httpClient.Do(req)
	g.Expect(err).NotTo(HaveOccurred())
	defer response.Body.Close()
	g.Expect(response.StatusCode).To(Equal(http.StatusOK))

	body, err := io.ReadAll(response.Body)
	g.Expect(err).NotTo(HaveOccurred())

	var respMap map[string]any
	err = json.Unmarshal(body, &respMap)
	g.Expect(err).NotTo(HaveOccurred())
	idToken, ok := respMap["id_token"].(string)
	g.Expect(ok).To(BeTrue(), "id_token not found or not a string")
	refreshToken, ok := respMap["refresh_token"].(string)
	g.Expect(ok).To(BeTrue(), "refresh_token not found or not a string")

	tokenCache := fmt.Sprintf(`{"id_token":"%s","refresh_token":"%s"}`, idToken, refreshToken)
	// The CI job that uses Keycloak external OIDC already sets Keycloak token lifetime proper to run case.
	// "type Key" is copied from https://github.com/openshift/oc/blob/master/pkg/cli/gettoken/tokencache/tokencache.go
	// We must keep the def of "type Key" as exactly same as original oc repo so that EncodeToString generates correct output
	type Key struct {
		IssuerURL string
		ClientID  string
	}

	key := Key{IssuerURL: authConfig.IssuerURL, ClientID: oidcClientID}
	s := sha256.New()
	e := gob.NewEncoder(s)
	err = e.Encode(&key)
	g.Expect(err).NotTo(HaveOccurred())

	tokenCacheFile := hex.EncodeToString(s.Sum(nil))
	rootDir := os.Getenv("SHARED_DIR")
	tokenCacheDir, err := os.MkdirTemp(rootDir, username)
	t.Cleanup(func() {
		_ = os.RemoveAll(tokenCacheDir)
	})
	g.Expect(err).NotTo(HaveOccurred())
	err = os.Mkdir(tokenCacheDir+"/oc", 0700)
	g.Expect(err).NotTo(HaveOccurred())
	err = os.WriteFile(filepath.Join(tokenCacheDir, "oc", tokenCacheFile), []byte(tokenCache), 0600)
	g.Expect(err).NotTo(HaveOccurred())

	clientConfigForExtOIDCUser := GetClientConfigForKeycloakOIDCUser(clientCfg, authConfig, tokenCacheDir)
	authClient, err := kauthnv1typedclient.NewForConfig(clientConfigForExtOIDCUser)
	g.Expect(err).NotTo(HaveOccurred())

	selfSubjectReview, err := authClient.SelfSubjectReviews().Create(ctx, &kauthnv1.SelfSubjectReview{}, metav1.CreateOptions{})
	g.Expect(err).NotTo(HaveOccurred())

	t.Logf("Detected external OIDC cluster using Keycloak as the provider. The user is now %q", selfSubjectReview.Status.UserInfo.Username)
	return clientConfigForExtOIDCUser
}

// GetClientConfigForKeycloakOIDCUser gets a client config for an external OIDC cluster
func GetClientConfigForKeycloakOIDCUser(clientCfg *rest.Config, authConfig *ExtOIDCConfig, tokenCacheDir string) *rest.Config {
	userClientConfig := rest.AnonymousClientConfig(rest.CopyConfig(clientCfg))
	args := []string{
		"get-token",
		"--issuer-url=" + authConfig.IssuerURL,
		"--client-id=" + authConfig.CliClientID,
		"--extra-scopes=email,profile",
		"--callback-address=127.0.0.1:0",
		"--certificate-authority=" + authConfig.IssuerCABundleFile,
	}

	userClientConfig.ExecProvider = &clientcmdapi.ExecConfig{
		APIVersion: "client.authentication.k8s.io/v1",
		Command:    "oc",
		Args:       args,
		// We can't use os.Setenv("KUBECACHEDIR", tokenCacheDir), so we use "ExecEnvVar" that ensures each
		// single user has unique cache path to avoid the parallel running users mess up the same cache path,
		// because the cache file name is decided by the issuer URL & client ID provided in CLI
		Env: []clientcmdapi.ExecEnvVar{
			{Name: "KUBECACHEDIR", Value: tokenCacheDir},
		},
		InstallHint:        "Please be sure that oc is defined in $PATH to be executed as credentials exec plugin",
		InteractiveMode:    clientcmdapi.IfAvailableExecInteractiveMode,
		ProvideClusterInfo: false,
	}

	return userClientConfig
}

// KeycloakAdminClient provides methods to interact with Keycloak Admin REST API
type KeycloakAdminClient struct {
	BaseURL    string
	AdminToken string
	HTTPClient *http.Client
	AdminUser  string
	AdminPass  string
	CACertFile string
}

// KeycloakUser represents a Keycloak user
type KeycloakUser struct {
	Username      string `json:"username"`
	Enabled       bool   `json:"enabled"`
	FirstName     string `json:"firstName,omitempty"`
	LastName      string `json:"lastName,omitempty"`
	Email         string `json:"email,omitempty"`
	EmailVerified bool   `json:"emailVerified,omitempty"`
}

// KeycloakGroup represents a Keycloak group
type KeycloakGroup struct {
	Name string `json:"name"`
}

// KeycloakCredential represents a user password credential
type KeycloakCredential struct {
	Type      string `json:"type"`
	Value     string `json:"value"`
	Temporary bool   `json:"temporary"`
}

// KeycloakClient represents a Keycloak client
type KeycloakClient struct {
	ID       string `json:"id"`
	ClientID string `json:"clientId"`
}

// KeycloakProtocolMapper represents a protocol mapper
type KeycloakProtocolMapper struct {
	Name            string            `json:"name"`
	Protocol        string            `json:"protocol"`
	ProtocolMapper  string            `json:"protocolMapper"`
	ConsentRequired bool              `json:"consentRequired"`
	Config          map[string]string `json:"config"`
}

// NewKeycloakAdminClient creates a new Keycloak admin client
func NewKeycloakAdminClient(baseURL, adminUser, adminPass, caCertFile string) *KeycloakAdminClient {
	return &KeycloakAdminClient{
		BaseURL:    baseURL,
		AdminUser:  adminUser,
		AdminPass:  adminPass,
		CACertFile: caCertFile,
		HTTPClient: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true,
				},
			},
		},
	}
}

// GetAdminToken obtains an admin access token
func (kc *KeycloakAdminClient) GetAdminToken(ctx context.Context) error {
	tokenURL := fmt.Sprintf("%s/realms/master/protocol/openid-connect/token", kc.BaseURL)

	formData := url.Values{
		"client_id":  []string{"admin-cli"},
		"grant_type": []string{"password"},
		"username":   []string{kc.AdminUser},
		"password":   []string{kc.AdminPass},
	}

	req, err := http.NewRequestWithContext(ctx, "POST", tokenURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Body = io.NopCloser(strings.NewReader(formData.Encode()))

	resp, err := kc.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to get admin token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to get admin token, status: %d, body: %s", resp.StatusCode, string(body))
	}

	var tokenResp map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return fmt.Errorf("failed to decode token response: %w", err)
	}

	accessToken, ok := tokenResp["access_token"].(string)
	if !ok {
		return fmt.Errorf("access_token not found in response")
	}

	kc.AdminToken = accessToken
	return nil
}

// CreateGroup creates a new group in Keycloak
func (kc *KeycloakAdminClient) CreateGroup(ctx context.Context, groupName string) (string, error) {
	groupURL := fmt.Sprintf("%s/admin/realms/master/groups", kc.BaseURL)

	group := KeycloakGroup{Name: groupName}
	groupJSON, err := json.Marshal(group)
	if err != nil {
		return "", fmt.Errorf("failed to marshal group: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", groupURL, strings.NewReader(string(groupJSON)))
	if err != nil {
		return "", fmt.Errorf("failed to create group request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+kc.AdminToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := kc.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to create group: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("failed to create group, status: %d, body: %s", resp.StatusCode, string(body))
	}

	// Extract group ID from Location header
	location := resp.Header.Get("Location")
	if location == "" {
		return "", fmt.Errorf("location header not found in response")
	}

	// Location format: https://host/admin/realms/master/groups/{groupId}
	parts := strings.Split(location, "/")
	if len(parts) == 0 {
		return "", fmt.Errorf("failed to parse group ID from location: %s", location)
	}
	groupID := parts[len(parts)-1]

	return groupID, nil
}

// CreateUser creates a new user in Keycloak
func (kc *KeycloakAdminClient) CreateUser(ctx context.Context, user KeycloakUser) (string, error) {
	userURL := fmt.Sprintf("%s/admin/realms/master/users", kc.BaseURL)

	userJSON, err := json.Marshal(user)
	if err != nil {
		return "", fmt.Errorf("failed to marshal user: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", userURL, strings.NewReader(string(userJSON)))
	if err != nil {
		return "", fmt.Errorf("failed to create user request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+kc.AdminToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := kc.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to create user: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("failed to create user, status: %d, body: %s", resp.StatusCode, string(body))
	}

	// Extract user ID from Location header
	location := resp.Header.Get("Location")
	if location == "" {
		return "", fmt.Errorf("location header not found in response")
	}

	// Location format: https://host/admin/realms/master/users/{userId}
	parts := strings.Split(location, "/")
	if len(parts) == 0 {
		return "", fmt.Errorf("failed to parse user ID from location: %s", location)
	}
	userID := parts[len(parts)-1]

	return userID, nil
}

// SetUserPassword sets a user's password
func (kc *KeycloakAdminClient) SetUserPassword(ctx context.Context, userID, password string, temporary bool) error {
	passwordURL := fmt.Sprintf("%s/admin/realms/master/users/%s/reset-password", kc.BaseURL, userID)

	credential := KeycloakCredential{
		Type:      "password",
		Value:     password,
		Temporary: temporary,
	}

	credJSON, err := json.Marshal(credential)
	if err != nil {
		return fmt.Errorf("failed to marshal credential: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "PUT", passwordURL, strings.NewReader(string(credJSON)))
	if err != nil {
		return fmt.Errorf("failed to create password request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+kc.AdminToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := kc.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to set password: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to set password, status: %d, body: %s", resp.StatusCode, string(body))
	}

	return nil
}

// AddUserToGroup adds a user to a group
func (kc *KeycloakAdminClient) AddUserToGroup(ctx context.Context, userID, groupID string) error {
	groupURL := fmt.Sprintf("%s/admin/realms/master/users/%s/groups/%s", kc.BaseURL, userID, groupID)

	req, err := http.NewRequestWithContext(ctx, "PUT", groupURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create add-to-group request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+kc.AdminToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := kc.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to add user to group: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to add user to group, status: %d, body: %s", resp.StatusCode, string(body))
	}

	return nil
}

// GetClientByClientID retrieves a client's internal ID by its clientId
func (kc *KeycloakAdminClient) GetClientByClientID(ctx context.Context, clientID string) (string, error) {
	clientsURL := fmt.Sprintf("%s/admin/realms/master/clients?clientId=%s", kc.BaseURL, url.QueryEscape(clientID))

	req, err := http.NewRequestWithContext(ctx, "GET", clientsURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create get-client request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+kc.AdminToken)

	resp, err := kc.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to get client: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("failed to get client, status: %d, body: %s", resp.StatusCode, string(body))
	}

	var clients []KeycloakClient
	if err := json.NewDecoder(resp.Body).Decode(&clients); err != nil {
		return "", fmt.Errorf("failed to decode clients response: %w", err)
	}

	if len(clients) == 0 {
		return "", fmt.Errorf("client not found: %s", clientID)
	}

	return clients[0].ID, nil
}

// DeleteUser deletes a user from Keycloak
func (kc *KeycloakAdminClient) DeleteUser(ctx context.Context, userID string) error {
	userURL := fmt.Sprintf("%s/admin/realms/master/users/%s", kc.BaseURL, userID)

	req, err := http.NewRequestWithContext(ctx, "DELETE", userURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create delete-user request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+kc.AdminToken)

	resp, err := kc.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to delete user: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to delete user, status: %d, body: %s", resp.StatusCode, string(body))
	}

	return nil
}

// DeleteGroup deletes a group from Keycloak
func (kc *KeycloakAdminClient) DeleteGroup(ctx context.Context, groupID string) error {
	groupURL := fmt.Sprintf("%s/admin/realms/master/groups/%s", kc.BaseURL, groupID)

	req, err := http.NewRequestWithContext(ctx, "DELETE", groupURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create delete-group request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+kc.AdminToken)

	resp, err := kc.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to delete group: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to delete group, status: %d, body: %s", resp.StatusCode, string(body))
	}

	return nil
}

// GetUserByUsername retrieves a user ID by username
func (kc *KeycloakAdminClient) GetUserByUsername(ctx context.Context, username string) (string, error) {
	usersURL := fmt.Sprintf("%s/admin/realms/master/users?username=%s&exact=true", kc.BaseURL, url.QueryEscape(username))

	req, err := http.NewRequestWithContext(ctx, "GET", usersURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create get-user request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+kc.AdminToken)

	resp, err := kc.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to get user: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("failed to get user, status: %d, body: %s", resp.StatusCode, string(body))
	}

	var users []struct {
		ID       string `json:"id"`
		Username string `json:"username"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&users); err != nil {
		return "", fmt.Errorf("failed to decode users response: %w", err)
	}

	if len(users) == 0 {
		return "", fmt.Errorf("user not found: %s", username)
	}

	return users[0].ID, nil
}

// GetGroupByName retrieves a group ID by name
func (kc *KeycloakAdminClient) GetGroupByName(ctx context.Context, groupName string) (string, error) {
	groupsURL := fmt.Sprintf("%s/admin/realms/master/groups", kc.BaseURL)

	req, err := http.NewRequestWithContext(ctx, "GET", groupsURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create get-groups request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+kc.AdminToken)

	resp, err := kc.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to get groups: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("failed to get groups, status: %d, body: %s", resp.StatusCode, string(body))
	}

	var groups []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&groups); err != nil {
		return "", fmt.Errorf("failed to decode groups response: %w", err)
	}

	for _, group := range groups {
		if group.Name == groupName {
			return group.ID, nil
		}
	}

	return "", fmt.Errorf("group not found: %s", groupName)
}

// CreateProtocolMapper creates a protocol mapper for a client
func (kc *KeycloakAdminClient) CreateProtocolMapper(ctx context.Context, clientID string, mapper KeycloakProtocolMapper) error {
	mapperURL := fmt.Sprintf("%s/admin/realms/master/clients/%s/protocol-mappers/models", kc.BaseURL, clientID)

	mapperJSON, err := json.Marshal(mapper)
	if err != nil {
		return fmt.Errorf("failed to marshal mapper: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", mapperURL, strings.NewReader(string(mapperJSON)))
	if err != nil {
		return fmt.Errorf("failed to create mapper request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+kc.AdminToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := kc.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to create mapper: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to create mapper, status: %d, body: %s", resp.StatusCode, string(body))
	}

	return nil
}

// SetupKeycloakTestEnvironment creates test users, groups, and protocol mappers
func SetupKeycloakTestEnvironment(t *testing.T, ctx context.Context, config *ExtOIDCConfig, adminUser, adminPass string, numUsers int) (string, error) {
	g := NewWithT(t)

	// Create admin client
	kc := NewKeycloakAdminClient(config.IssuerURL, adminUser, adminPass, config.IssuerCABundleFile)

	// Get admin token
	err := kc.GetAdminToken(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get admin token: %w", err)
	}

	// Create group
	t.Logf("Creating Keycloak group: keycloak-testgroup-1")
	groupID, err := kc.CreateGroup(ctx, "keycloak-testgroup-1")
	if err != nil {
		return "", fmt.Errorf("failed to create group: %w", err)
	}
	t.Logf("Created group with ID: %s", groupID)

	// Create users and add to group
	var users []string
	for i := 1; i <= numUsers; i++ {
		username := fmt.Sprintf("keycloak-testuser-%d", i)
		password := GenerateRandomPassword(12)

		user := KeycloakUser{
			Username:      username,
			Enabled:       true,
			FirstName:     username,
			LastName:      "KC",
			Email:         fmt.Sprintf("%s@example.com", username),
			EmailVerified: true,
		}

		t.Logf("Creating user: %s", username)
		userID, err := kc.CreateUser(ctx, user)
		if err != nil {
			return "", fmt.Errorf("failed to create user %s: %w", username, err)
		}

		// Set password
		err = kc.SetUserPassword(ctx, userID, password, false)
		if err != nil {
			return "", fmt.Errorf("failed to set password for user %s: %w", username, err)
		}

		// Add to group
		err = kc.AddUserToGroup(ctx, userID, groupID)
		if err != nil {
			return "", fmt.Errorf("failed to add user %s to group: %w", username, err)
		}

		users = append(users, fmt.Sprintf("%s:%s", username, password))
	}

	// Create protocol mappers for clients
	groupMapper := KeycloakProtocolMapper{
		Name:            "groupmapper",
		Protocol:        "openid-connect",
		ProtocolMapper:  "oidc-group-membership-mapper",
		ConsentRequired: false,
		Config: map[string]string{
			"full.path":            "false",
			"userinfo.token.claim": "true",
			"id.token.claim":       "true",
			"access.token.claim":   "false",
			"claim.name":           "groups",
		},
	}

	// Add group mapper to CLI client
	t.Logf("Adding group mapper to CLI client: %s", config.CliClientID)
	cliClientID, err := kc.GetClientByClientID(ctx, config.CliClientID)
	if err != nil {
		return "", fmt.Errorf("failed to get CLI client ID: %w", err)
	}
	err = kc.CreateProtocolMapper(ctx, cliClientID, groupMapper)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create protocol mapper for CLI client")

	// Add group mapper to console client
	t.Logf("Adding group mapper to console client: %s", config.ConsoleClientID)
	consoleClientID, err := kc.GetClientByClientID(ctx, config.ConsoleClientID)
	if err != nil {
		return "", fmt.Errorf("failed to get console client ID: %w", err)
	}
	err = kc.CreateProtocolMapper(ctx, consoleClientID, groupMapper)
	g.Expect(err).NotTo(HaveOccurred(), "failed to create protocol mapper for console client")

	// Return users in format "user1:pass1,user2:pass2,..."
	return strings.Join(users, ","), nil
}

// GenerateRandomPassword generates a random password
func GenerateRandomPassword(length int) string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, length)
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}

// SetupKeycloakAdminClientFromCluster retrieves Keycloak admin credentials from the cluster and creates an admin client
func SetupKeycloakAdminClientFromCluster(ctx context.Context, t *testing.T, mgtClient crclient.Client, config *ExtOIDCConfig) (*KeycloakAdminClient, error) {
	g := NewWithT(t)

	// Tests are ran on both AWS and Azure AKS clusters respectively.
	// However, Keycloak credentials are stored differently on both.

	// On AWS, both admin username and password credentials are stored
	// via a StatefulSet called 'keycloak' in the 'keycloak' namespace.

	// On AKS, the admin username is stored in a config map called
	// 'keycloak-env-vars' in 'keycloak' namespace via data.KC_BOOTSTAP_ADMIN_USERNAME,
	// and the admin password is stored in a secret called 'keycloak'
	// in the 'keycloak' namespace via data.admin-password .
	// https://github.com/bitnami/charts/tree/main/bitnami/keycloak/templates

	adminUser, adminPass := "", ""

	// Try AWS approach first: read from StatefulSet environment variables
	t.Logf("Retrieving Keycloak admin credentials from StatefulSet (AWS approach)")
	sts := &appsv1.StatefulSet{}
	err := mgtClient.Get(ctx, crclient.ObjectKey{
		Namespace: "keycloak",
		Name:      "keycloak",
	}, sts)
	if err == nil {
		// StatefulSet exists, try to read credentials from environment variables
		for _, env := range sts.Spec.Template.Spec.Containers[0].Env {
			if env.Name == "KC_BOOTSTRAP_ADMIN_USERNAME" {
				adminUser = env.Value
			}
			if env.Name == "KC_BOOTSTRAP_ADMIN_PASSWORD" {
				adminPass = env.Value
			}
		}
	}

	// If credentials not found in StatefulSet, try AKS approach: ConfigMap + Secret
	if adminUser == "" || adminPass == "" {
		t.Logf("Credentials not found in StatefulSet, trying AKS approach (ConfigMap + Secret)")

		// Get admin username from ConfigMap
		cm := &corev1.ConfigMap{}
		err = mgtClient.Get(ctx, crclient.ObjectKey{
			Namespace: "keycloak",
			Name:      "keycloak-env-vars",
		}, cm)
		if err == nil && cm.Data != nil {
			adminUser = cm.Data["KC_BOOTSTRAP_ADMIN_USERNAME"]
		}

		// Get admin password from Secret
		secret := &corev1.Secret{}
		err = mgtClient.Get(ctx, crclient.ObjectKey{
			Namespace: "keycloak",
			Name:      "keycloak",
		}, secret)
		if err == nil && secret.Data != nil {
			adminPass = string(secret.Data["admin-password"])
		}
	}

	// Verify we found both credentials
	if adminUser == "" || adminPass == "" {
		return nil, fmt.Errorf("could not find Keycloak admin credentials in StatefulSet (AWS) or ConfigMap+Secret (AKS)")
	}

	t.Logf("Successfully retrieved Keycloak admin credentials (username: %s)", adminUser)

	// Trim /realms/master from issuerURL
	baseURL := strings.TrimSuffix(config.IssuerURL, "/realms/master")
	kc := NewKeycloakAdminClient(baseURL, adminUser, adminPass, config.IssuerCABundleFile)

	// Verify access by getting admin token
	err = kc.GetAdminToken(ctx)
	g.Expect(err).NotTo(HaveOccurred(), "failed to get admin token")

	t.Logf("Successfully created Keycloak admin client")
	return kc, nil
}

// TestResources tracks resources created during a test for cleanup
type TestResources struct {
	AdminClient *KeycloakAdminClient
	UserIDs     []string
	GroupIDs    []string
	UserCreds   map[string]string // username -> password
}

// NewTestResources creates a new TestResources tracker
func NewTestResources(adminClient *KeycloakAdminClient) *TestResources {
	return &TestResources{
		AdminClient: adminClient,
		UserIDs:     []string{},
		GroupIDs:    []string{},
		UserCreds:   make(map[string]string),
	}
}

// CreateTestUser creates a user and tracks it for cleanup
func (tr *TestResources) CreateTestUser(ctx context.Context, t *testing.T, username, email, password string) (string, error) {
	return tr.CreateTestUserWithEmailVerification(ctx, t, username, email, password, true)
}

// CreateTestUserWithEmailVerification creates a user with specific email verification status and tracks it for cleanup
func (tr *TestResources) CreateTestUserWithEmailVerification(ctx context.Context, t *testing.T, username, email, password string, emailVerified bool) (string, error) {
	user := KeycloakUser{
		Username:      username,
		Enabled:       true,
		FirstName:     username,
		LastName:      "Test",
		Email:         email,
		EmailVerified: emailVerified,
	}

	userID, err := tr.AdminClient.CreateUser(ctx, user)
	if err != nil {
		return "", err
	}

	// Set password
	err = tr.AdminClient.SetUserPassword(ctx, userID, password, false)
	if err != nil {
		return "", err
	}

	// Track for cleanup
	tr.UserIDs = append(tr.UserIDs, userID)
	tr.UserCreds[username] = password

	t.Logf("Created test user: %s (ID: %s, email_verified: %v)", username, userID, emailVerified)
	return userID, nil
}

// CreateTestGroup creates a group and tracks it for cleanup
func (tr *TestResources) CreateTestGroup(ctx context.Context, t *testing.T, groupName string) (string, error) {
	groupID, err := tr.AdminClient.CreateGroup(ctx, groupName)
	if err != nil {
		return "", err
	}

	// Track for cleanup
	tr.GroupIDs = append(tr.GroupIDs, groupID)

	t.Logf("Created test group: %s (ID: %s)", groupName, groupID)
	return groupID, nil
}

// CreateTestUserWithRandomCredentials creates a user with generated credentials and tracks it for cleanup
func (tr *TestResources) CreateTestUserWithRandomCredentials(ctx context.Context, t *testing.T, usernamePrefix string) (userID, username, email, password string, err error) {
	username = usernamePrefix + "-" + GenerateRandomPassword(8)
	email = username + "@test.example.com"
	password = GenerateRandomPassword(16)

	userID, err = tr.CreateTestUser(ctx, t, username, email, password)
	return userID, username, email, password, err
}

// GetTestUsersString returns users in format "user1:pass1,user2:pass2,..."
func (tr *TestResources) GetTestUsersString() string {
	var users []string
	for username, password := range tr.UserCreds {
		users = append(users, fmt.Sprintf("%s:%s", username, password))
	}
	return strings.Join(users, ",")
}

// Cleanup deletes all tracked resources
func (tr *TestResources) Cleanup(ctx context.Context, t *testing.T) {
	t.Logf("Cleaning up test resources: %d users, %d groups", len(tr.UserIDs), len(tr.GroupIDs))

	if err := tr.AdminClient.GetAdminToken(ctx); err != nil {
		t.Logf("Warning: failed to refresh admin token: %v", err)
	}

	// Delete users
	for _, userID := range tr.UserIDs {
		if err := tr.AdminClient.DeleteUser(ctx, userID); err != nil {
			t.Logf("Warning: failed to delete user %s: %v", userID, err)
		} else {
			t.Logf("Deleted user: %s", userID)
		}
	}

	// Delete groups
	for _, groupID := range tr.GroupIDs {
		if err := tr.AdminClient.DeleteGroup(ctx, groupID); err != nil {
			t.Logf("Warning: failed to delete group %s: %v", groupID, err)
		} else {
			t.Logf("Deleted group: %s", groupID)
		}
	}

	// Clear tracking
	tr.UserIDs = []string{}
	tr.GroupIDs = []string{}
	tr.UserCreds = make(map[string]string)
}

// AuthenticatedTestUser contains the results of setting up an authenticated test user
type AuthenticatedTestUser struct {
	Username          string
	Email             string
	Password          string
	UserID            string
	GroupID           string
	GroupName         string
	KubeConfig        *rest.Config
	AuthClient        kauthnv1typedclient.AuthenticationV1Interface
	SelfSubjectReview *kauthnv1.SelfSubjectReview
}

// TryAuthenticateUser attempts to authenticate a user and returns error if authentication fails
// This is useful for negative testing where you expect authentication to fail
func (tr *TestResources) TryAuthenticateUser(
	ctx context.Context,
	t *testing.T,
	username string,
	password string,
	clientCfg *rest.Config,
	extOIDCConfig *ExtOIDCConfig,
) error {
	// This function replicates ChangeUserForKeycloakExtOIDC but returns errors
	// instead of using gomega assertions, allowing negative testing scenarios

	if extOIDCConfig == nil {
		return fmt.Errorf("extOIDCConfig is nil")
	}
	if extOIDCConfig.ExternalOIDCProvider != ProviderKeycloak {
		return fmt.Errorf("expected Keycloak provider, got %s", extOIDCConfig.ExternalOIDCProvider)
	}

	// Step 1: Get OIDC token from Keycloak
	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: true,
			},
		},
	}

	requestURL := extOIDCConfig.IssuerURL + "/protocol/openid-connect/token"
	oidcClientID := extOIDCConfig.CliClientID
	if oidcClientID == "" {
		return fmt.Errorf("oidcClientID is empty")
	}

	formData := url.Values{
		"client_id":  []string{oidcClientID},
		"grant_type": []string{"password"},
		"password":   []string{password},
		"scope":      []string{"openid email profile"},
		"username":   []string{username},
	}

	response, err := httpClient.PostForm(requestURL, formData)
	if err != nil {
		return fmt.Errorf("failed to POST to token endpoint: %w", err)
	}
	defer response.Body.Close()

	// Authentication can fail at Keycloak level (e.g., email_verified=false)
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		return fmt.Errorf("keycloak authentication failed with status %d: %s", response.StatusCode, string(body))
	}

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	var respMap map[string]any
	err = json.Unmarshal(body, &respMap)
	if err != nil {
		return fmt.Errorf("failed to unmarshal token response: %w", err)
	}

	idToken, ok := respMap["id_token"].(string)
	if !ok {
		return fmt.Errorf("id_token not found or not a string in response")
	}

	// Step 2: Try to authenticate with K8s using the ID token directly
	// We use the token as a bearer token instead of going through the exec plugin,
	// which would attempt an authorization code flow (browser-based) that requires
	// user interaction. Using the token directly allows us to capture validation
	// errors from the Kubernetes API server.
	// Use AnonymousClientConfig to clear all auth credentials (certs, tokens) from the admin config
	clientConfigForExtOIDCUser := rest.AnonymousClientConfig(rest.CopyConfig(clientCfg))
	clientConfigForExtOIDCUser.BearerToken = idToken

	authClient, err := kauthnv1typedclient.NewForConfig(clientConfigForExtOIDCUser)
	if err != nil {
		return fmt.Errorf("failed to create auth client: %w", err)
	}

	// Authentication can fail at K8s level due to claim validation rules or user validation rules
	_, err = authClient.SelfSubjectReviews().Create(ctx, &kauthnv1.SelfSubjectReview{}, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("kubernetes authentication failed: %w", err)
	}

	return nil
}

// SetupAuthenticatedUserWithGroup creates a test user, group, adds user to group, authenticates, and gets self subject review
// This is a complete setup for testing authentication and authorization scenarios
func (tr *TestResources) SetupAuthenticatedUserWithGroup(
	ctx context.Context,
	t *testing.T,
	usernamePrefix string,
	groupNamePrefix string,
	clientCfg *rest.Config,
	extOIDCConfig *ExtOIDCConfig,
) (*AuthenticatedTestUser, error) {
	g := NewWithT(t)

	// Create test group
	groupName := groupNamePrefix + "-" + GenerateRandomPassword(8)
	groupID, err := tr.CreateTestGroup(ctx, t, groupName)
	if err != nil {
		return nil, fmt.Errorf("failed to create test group: %w", err)
	}
	t.Logf("Created test group: %s (ID: %s)", groupName, groupID)

	// Create test user with specific email
	username := usernamePrefix + "-" + GenerateRandomPassword(8)
	email := username + "@test.example.com"
	password := GenerateRandomPassword(16)
	userID, err := tr.CreateTestUser(ctx, t, username, email, password)
	if err != nil {
		return nil, fmt.Errorf("failed to create test user: %w", err)
	}
	t.Logf("Created test user: %s (email: %s, ID: %s)", username, email, userID)

	// Add user to group
	err = tr.AdminClient.AddUserToGroup(ctx, userID, groupID)
	if err != nil {
		return nil, fmt.Errorf("failed to add user to group: %w", err)
	}
	t.Logf("Added user %s to group %s", username, groupName)

	// Authenticate as test user
	testAuthConfig := *extOIDCConfig
	testAuthConfig.TestUsers = username + ":" + password
	testUserKubeConfig := ChangeUserForKeycloakExtOIDC(t, ctx, clientCfg, &testAuthConfig)
	testAuthClient, err := kauthnv1typedclient.NewForConfig(testUserKubeConfig)
	g.Expect(err).NotTo(HaveOccurred())

	// Get self subject review
	selfSubjectReview, err := testAuthClient.SelfSubjectReviews().Create(ctx, &kauthnv1.SelfSubjectReview{}, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get self subject review: %w", err)
	}
	t.Logf("Test user self subject review: %+v", selfSubjectReview.Status.UserInfo)

	return &AuthenticatedTestUser{
		Username:          username,
		Email:             email,
		Password:          password,
		UserID:            userID,
		GroupID:           groupID,
		GroupName:         groupName,
		KubeConfig:        testUserKubeConfig,
		AuthClient:        testAuthClient,
		SelfSubjectReview: selfSubjectReview,
	}, nil
}
