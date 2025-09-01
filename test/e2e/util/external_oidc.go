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
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	configv1 "github.com/openshift/api/config/v1"
	configv1typedclient "github.com/openshift/client-go/config/clientset/versioned/typed/config/v1"

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
	return &configv1.AuthenticationSpec{
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
						TokenClaimMapping: configv1.TokenClaimMapping{
							Claim: "email",
						},
						PrefixPolicy: configv1.Prefix,
						Prefix: &configv1.UsernamePrefix{
							PrefixString: config.UserPrefix,
						},
					},
					UID: &configv1.TokenClaimOrExpressionMapping{
						Expression: fmt.Sprintf(`"%s" + claims.sub + "%s"`, ExternalOIDCUIDExpressionPrefix, ExternalOIDCUIDExpressionSubfix),
					},
					Extra: []configv1.ExtraMapping{
						{
							Key:             ExternalOIDCExtraKeyBar,
							ValueExpression: fmt.Sprintf(`"%s"`, ExternalOIDCExtraKeyBarValueExpression),
						},
						{
							Key:             ExternalOIDCExtraKeyFoo,
							ValueExpression: ExternalOIDCExtraKeyFooValueExpression,
						},
					},
				},
			},
		},
	}
}

// ValidateAuthenticationSpec validates the external OIDC configuration and the expected HostedCluster authentication configuration before running the test
func ValidateAuthenticationSpec(t *testing.T, ctx context.Context, client crclient.Client, hostedCluster *hyperv1.HostedCluster, config *ExtOIDCConfig) {
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
	g.Expect(actualAuth.OIDCProviders[0].ClaimMappings.Extra).NotTo(BeEmpty())
	g.Expect(actualAuth.OIDCProviders[0].ClaimMappings.UID).NotTo(BeNil())

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
func IsExternalOIDCCluster(t *testing.T, ctx context.Context, clientCfg *rest.Config) (bool, error) {
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
func ChangeClientForKeycloakExtOIDC(t *testing.T, ctx context.Context, clientCfg *rest.Config, authConfig *ExtOIDCConfig) crclient.Client {
	g := NewWithT(t)
	newConfig := ChangeUserForKeycloakExtOIDC(t, ctx, clientCfg, authConfig)
	client, err := crclient.New(newConfig, crclient.Options{Scheme: scheme})
	g.Expect(err).NotTo(HaveOccurred(), "could not create guest client using the new config")
	return client
}

// ChangeUserForKeycloakExtOIDC changes the user of current CLI session for a Keycloak external OIDC cluster
func ChangeUserForKeycloakExtOIDC(t *testing.T, ctx context.Context, clientCfg *rest.Config, authConfig *ExtOIDCConfig) *rest.Config {
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

	response, err := httpClient.PostForm(requestURL, formData)
	g.Expect(err).NotTo(HaveOccurred())
	defer func() {
		_ = response.Body.Close()
	}()
	g.Expect(response.StatusCode).To(Equal(http.StatusOK))

	body, err := io.ReadAll(response.Body)
	g.Expect(err).NotTo(HaveOccurred())

	var respMap map[string]interface{}
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
		"--callback-address=127.0.0.1:8080",
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
