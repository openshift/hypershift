//go:build e2ev2

package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/hypershift-operator/controllers/manifests"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	v2util "github.com/openshift/hypershift/test/e2e/v2/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	externalOIDCTransitionPollInterval = 5 * time.Second
	externalOIDCTransitionTimeout      = 10 * time.Minute
)

type externalOIDCSetup struct {
	sharedDir      string
	keycloakConfig *v2util.KeycloakConfig
}

func (s *externalOIDCSetup) preCreate(ctx context.Context, cl crclient.Client) error {
	kcConfig, err := v2util.DeployKeycloak(ctx, cl, "https://placeholder.example.com/auth/callback")
	if err != nil {
		return fmt.Errorf("deploying keycloak in pre-create: %w", err)
	}
	s.keycloakConfig = kcConfig
	log.Printf("Keycloak deployed: issuer=%s", kcConfig.IssuerURL)
	return nil
}

func (s *externalOIDCSetup) postVersionRollout(ctx context.Context, cl crclient.Client, namespace string, clusterNames map[string]string) error {
	name, ok := clusterNames["external-oidc"]
	if !ok {
		return nil
	}

	hc := &hyperv1.HostedCluster{}
	if err := cl.Get(ctx, crclient.ObjectKey{Namespace: namespace, Name: name}, hc); err != nil {
		return fmt.Errorf("getting HostedCluster %s/%s for OIDC setup: %w", namespace, name, err)
	}
	if s.keycloakConfig == nil {
		return fmt.Errorf("keycloak config not available; PreCreate must run before PostVersionRollout")
	}

	consoleRedirectURI := fmt.Sprintf("https://console-openshift-console.apps.%s.%s/auth/callback",
		hc.Name, hc.Spec.DNS.BaseDomain)
	if err := v2util.UpdateKeycloakConsoleClient(ctx, consoleRedirectURI); err != nil {
		return fmt.Errorf("updating keycloak console client redirect URI: %w", err)
	}

	caCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: "oidc-ca", Namespace: namespace},
		Data:       map[string]string{"ca-bundle.crt": string(s.keycloakConfig.CABundle)},
	}
	if err := v2util.CreateOrUpdate(ctx, cl, caCM); err != nil {
		return fmt.Errorf("creating OIDC CA configmap: %w", err)
	}

	consoleSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "console-secret", Namespace: namespace},
		Type:       corev1.SecretTypeOpaque,
		StringData: map[string]string{"clientSecret": s.keycloakConfig.ConsoleClientSecret},
	}
	if err := v2util.CreateOrUpdate(ctx, cl, consoleSecret); err != nil {
		return fmt.Errorf("creating console client secret: %w", err)
	}

	extOIDCConfig := &e2eutil.ExtOIDCConfig{
		ExternalOIDCProvider:     e2eutil.ProviderKeycloak,
		OIDCProviderName:         "keycloak oidc server",
		CliClientID:              s.keycloakConfig.CLIClientID,
		ConsoleClientID:          s.keycloakConfig.ConsoleClientID,
		IssuerURL:                s.keycloakConfig.IssuerURL,
		GroupPrefix:              "oidc-groups-test:",
		UserPrefix:               "oidc-user-test:",
		ConsoleClientSecretName:  "console-secret",
		ConsoleClientSecretValue: s.keycloakConfig.ConsoleClientSecret,
		IssuerCAConfigmapName:    "oidc-ca",
		TestUsers:                s.keycloakConfig.TestUsers,
	}

	patch := crclient.MergeFrom(hc.DeepCopy())
	if hc.Spec.Configuration == nil {
		hc.Spec.Configuration = &hyperv1.ClusterConfiguration{}
	}
	hc.Spec.Configuration.Authentication = extOIDCConfig.GetAuthenticationConfig()
	if err := cl.Patch(ctx, hc, patch); err != nil {
		return fmt.Errorf("patching HostedCluster %s/%s with OIDC config: %w", namespace, name, err)
	}
	log.Printf("Patched HostedCluster %s/%s with External OIDC config", namespace, name)

	controlPlaneNamespace := manifests.HostedControlPlaneNamespace(namespace, name)
	if err := wait.PollUntilContextTimeout(ctx, externalOIDCTransitionPollInterval, externalOIDCTransitionTimeout, true,
		func(ctx context.Context) (bool, error) {
			return externalOIDCTransitionComplete(ctx, cl, controlPlaneNamespace, s.keycloakConfig.IssuerURL)
		}); err != nil {
		return fmt.Errorf("waiting for HostedCluster %s/%s External OIDC transition: %w", namespace, name, err)
	}
	log.Printf("HostedCluster %s/%s completed the External OIDC transition", namespace, name)

	if s.sharedDir == "" {
		return nil
	}
	caPath := filepath.Join(s.sharedDir, "external_oidc_ca_bundle")
	if err := os.WriteFile(caPath, s.keycloakConfig.CABundle, 0600); err != nil {
		return fmt.Errorf("writing CA bundle to %s: %w", caPath, err)
	}
	testUsersPath := filepath.Join(s.sharedDir, "external_oidc_test_users")
	if err := os.WriteFile(testUsersPath, []byte(s.keycloakConfig.TestUsers), 0600); err != nil {
		return fmt.Errorf("writing test users to %s: %w", testUsersPath, err)
	}
	log.Printf("Wrote External OIDC CA bundle and test users to SHARED_DIR")
	return nil
}

func externalOIDCTransitionComplete(ctx context.Context, cl crclient.Client, controlPlaneNamespace, issuerURL string) (bool, error) {
	for _, name := range []string{"oauth-openshift", "openshift-oauth-apiserver"} {
		deployment := &appsv1.Deployment{}
		err := cl.Get(ctx, crclient.ObjectKey{Namespace: controlPlaneNamespace, Name: name}, deployment)
		switch {
		case err == nil:
			return false, nil
		case apierrors.IsNotFound(err):
			continue
		default:
			return false, fmt.Errorf("getting Deployment %s/%s: %w", controlPlaneNamespace, name, err)
		}
	}

	authConfigMap := &corev1.ConfigMap{}
	if err := cl.Get(ctx, crclient.ObjectKey{Namespace: controlPlaneNamespace, Name: "auth-config"}, authConfigMap); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("getting ConfigMap %s/auth-config: %w", controlPlaneNamespace, err)
	}
	var authConfig struct {
		JWT []struct {
			Issuer struct {
				URL string `json:"url"`
			} `json:"issuer"`
		} `json:"jwt"`
	}
	if err := json.Unmarshal([]byte(authConfigMap.Data["auth.json"]), &authConfig); err != nil {
		return false, fmt.Errorf("parsing ConfigMap %s/auth-config auth.json: %w", controlPlaneNamespace, err)
	}
	if len(authConfig.JWT) == 0 || authConfig.JWT[0].Issuer.URL != issuerURL {
		return false, nil
	}

	kasConfigMap := &corev1.ConfigMap{}
	if err := cl.Get(ctx, crclient.ObjectKey{Namespace: controlPlaneNamespace, Name: "kas-config"}, kasConfigMap); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("getting ConfigMap %s/kas-config: %w", controlPlaneNamespace, err)
	}
	var kasConfig struct {
		APIServerArguments map[string]json.RawMessage `json:"apiServerArguments"`
	}
	if err := json.Unmarshal([]byte(kasConfigMap.Data["config.json"]), &kasConfig); err != nil {
		return false, fmt.Errorf("parsing ConfigMap %s/kas-config config.json: %w", controlPlaneNamespace, err)
	}
	if _, hasWebhook := kasConfig.APIServerArguments["authentication-token-webhook-config-file"]; hasWebhook {
		return false, nil
	}

	return true, nil
}

func setupExternalOIDCTestEnv(sharedDir string) {
	caPath := filepath.Join(sharedDir, "external_oidc_ca_bundle")
	if _, err := os.Stat(caPath); err == nil {
		os.Setenv("E2E_EXTERNAL_OIDC_CA_BUNDLE_FILE", caPath)
	}
	if data, err := os.ReadFile(filepath.Join(sharedDir, "external_oidc_test_users")); err == nil {
		os.Setenv("E2E_EXTERNAL_OIDC_TEST_USERS", strings.TrimSpace(string(data)))
	}
}
