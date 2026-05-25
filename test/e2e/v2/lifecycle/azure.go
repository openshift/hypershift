//go:build e2ev2

package lifecycle

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	operatorv1 "github.com/openshift/api/operator/v1"
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	e2eutil "github.com/openshift/hypershift/test/e2e/util"
	v2util "github.com/openshift/hypershift/test/e2e/v2/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	defaultAzureCreds     = "/etc/hypershift-ci-jobs-self-managed-azure/credentials.json"
	defaultAzureLocation  = "centralus"
	defaultAzureDNSZoneRG = "os4-common"

	defaultOIDCIssuerURL      = "https://smazure.blob.core.windows.net/smazure"
	defaultSATokenKeyPath     = "/etc/hypershift-ci-jobs-self-managed-azure-e2e/serviceaccount-signer.private"
	defaultWorkloadIdentities = "/etc/hypershift-ci-jobs-self-managed-azure-e2e/workload-identities.json"
	defaultEncryptionKeyID    = "/etc/hypershift-ci-jobs-self-managed-azure-e2e/AZURE_ENCRYPTION_KEY_ID"
)

// AzurePlatformConfig holds Azure-specific configuration for the
// hypershift CLI.
type AzurePlatformConfig struct {
	creds              string
	location           string
	oidcIssuerURL      string
	saTokenKeyPath     string
	workloadIdentities string
	dnsZoneRG          string
	privateNATSubnetID string
	sharedDir          string
	encryptionKeyID    string

	marketplacePublisher string
	marketplaceOffer     string
	marketplaceSKU       string
	marketplaceVersion   string

	keycloakConfig *v2util.KeycloakConfig
}

// NewAzurePlatformConfig reads Azure-specific configuration from
// environment variables with CI defaults.
func NewAzurePlatformConfig(sharedDir string) *AzurePlatformConfig {
	cfg := &AzurePlatformConfig{
		creds:              envOrDefault("AZURE_CREDS", defaultAzureCreds),
		location:           envOrDefault("HYPERSHIFT_AZURE_LOCATION", defaultAzureLocation),
		oidcIssuerURL:      envOrDefault("AZURE_OIDC_ISSUER_URL", defaultOIDCIssuerURL),
		saTokenKeyPath:     envOrDefault("AZURE_SA_TOKEN_ISSUER_KEY_PATH", defaultSATokenKeyPath),
		workloadIdentities: envOrDefault("AZURE_WORKLOAD_IDENTITIES_FILE", defaultWorkloadIdentities),
		dnsZoneRG:          defaultAzureDNSZoneRG,
		sharedDir:          sharedDir,

		marketplacePublisher: os.Getenv("HYPERSHIFT_AZURE_MARKETPLACE_IMAGE_PUBLISHER"),
		marketplaceOffer:     os.Getenv("HYPERSHIFT_AZURE_MARKETPLACE_IMAGE_OFFER"),
		marketplaceSKU:       os.Getenv("HYPERSHIFT_AZURE_MARKETPLACE_IMAGE_SKU"),
		marketplaceVersion:   os.Getenv("HYPERSHIFT_AZURE_MARKETPLACE_IMAGE_VERSION"),
	}

	cfg.privateNATSubnetID = os.Getenv("AZURE_PRIVATE_NAT_SUBNET_ID")
	if cfg.privateNATSubnetID == "" && sharedDir != "" {
		if data, err := os.ReadFile(filepath.Join(sharedDir, "azure_private_nat_subnet_id")); err == nil {
			cfg.privateNATSubnetID = strings.TrimSpace(string(data))
		}
	}
	if cfg.privateNATSubnetID == "" {
		log.Printf("WARNING: AZURE_PRIVATE_NAT_SUBNET_ID is not set; private cluster creation will fail")
	}

	cfg.encryptionKeyID = os.Getenv("AZURE_ENCRYPTION_KEY_ID")
	if cfg.encryptionKeyID == "" {
		if data, err := os.ReadFile(defaultEncryptionKeyID); err == nil {
			cfg.encryptionKeyID = strings.TrimSpace(string(data))
		}
	}
	if cfg.encryptionKeyID == "" {
		log.Printf("WARNING: AZURE_ENCRYPTION_KEY_ID is not set; secret encryption tests will be skipped")
	}

	if cfg.marketplaceSKU == "" && cfg.marketplacePublisher != "" && sharedDir != "" {
		if data, err := os.ReadFile(filepath.Join(sharedDir, "azure-marketplace-image-sku")); err == nil {
			cfg.marketplaceSKU = strings.TrimSpace(string(data))
		}
	}
	if cfg.marketplaceVersion == "" && cfg.marketplacePublisher != "" && sharedDir != "" {
		if data, err := os.ReadFile(filepath.Join(sharedDir, "azure-marketplace-image-version")); err == nil {
			cfg.marketplaceVersion = strings.TrimSpace(string(data))
		}
	}

	return cfg
}

func (a *AzurePlatformConfig) Name() string { return "azure" }

func (a *AzurePlatformConfig) DefaultBaseDomain() string {
	return "hcp-sm-azure.azure.devcluster.openshift.com"
}

func (a *AzurePlatformConfig) ClusterSpecs(releaseImage, n1Image string) []ClusterSpec {
	var publicExtraArgs []string
	if a.encryptionKeyID != "" {
		publicExtraArgs = append(publicExtraArgs, "--encryption-key-id="+a.encryptionKeyID)
	}

	return []ClusterSpec{
		{
			Variant:    "public",
			OutputFile: "cluster-name-public",
			ExtraArgs:  publicExtraArgs,
		},
		{
			Variant:    "private",
			OutputFile: "cluster-name-private",
			ExtraArgs: []string{
				"--endpoint-access=Private",
				"--endpoint-access-private-nat-subnet-id=" + a.privateNATSubnetID,
				"--oauth-publishing-strategy=LoadBalancer",
			},
		},
		{
			Variant:    "oauth-lb",
			OutputFile: "cluster-name-oauth-lb",
			ExtraArgs:  []string{"--oauth-publishing-strategy=LoadBalancer"},
		},
		{
			Variant:      "upgrade",
			OutputFile:   "cluster-name-upgrade",
			ReleaseImage: n1Image,
			ExtraArgs:    []string{"--control-plane-availability-policy=HighlyAvailable"},
		},
		{
			Variant:    "autoscaling",
			OutputFile: "cluster-name-autoscaling",
		},
		{
			Variant:    "external-oidc",
			OutputFile: "cluster-name-external-oidc",
		},
	}
}

func (a *AzurePlatformConfig) CreateArgs() []string {
	args := []string{
		"--azure-creds=" + a.creds,
		"--location=" + a.location,
		"--oidc-issuer-url=" + a.oidcIssuerURL,
		"--sa-token-issuer-private-key-path=" + a.saTokenKeyPath,
		"--workload-identities-file=" + a.workloadIdentities,
		"--assign-service-principal-roles",
		"--dns-zone-rg-name=" + a.dnsZoneRG,
	}

	if a.marketplacePublisher != "" {
		args = append(args, "--marketplace-publisher="+a.marketplacePublisher)
		args = append(args, "--marketplace-offer="+a.marketplaceOffer)
		if a.marketplaceSKU != "" {
			args = append(args, "--marketplace-sku="+a.marketplaceSKU)
		}
		if a.marketplaceVersion != "" {
			args = append(args, "--marketplace-version="+a.marketplaceVersion)
		}
	}

	return args
}

// PreCreate deploys infrastructure that must be ready before clusters
// are created (e.g., the Keycloak OIDC provider for the external-oidc variant).
func (a *AzurePlatformConfig) PreCreate(ctx context.Context, cl crclient.WithWatch, namespace string) error {
	kcConfig, err := v2util.DeployKeycloak(ctx, cl, "https://placeholder.example.com/auth/callback")
	if err != nil {
		return fmt.Errorf("deploying keycloak in pre-create: %w", err)
	}
	a.keycloakConfig = kcConfig
	log.Printf("Keycloak deployed: issuer=%s", kcConfig.IssuerURL)
	return nil
}

// PostCreate runs variant-specific post-creation hooks for each cluster
// that was created by the lifecycle orchestrator.
func (a *AzurePlatformConfig) PostCreate(ctx context.Context, cl crclient.WithWatch, namespace string, clusterNames map[string]string) error {
	if publicName, ok := clusterNames["cluster-name-public"]; ok {
		if err := a.postCreatePublic(ctx, cl, namespace, publicName); err != nil {
			return err
		}
	}
	return nil
}

// PostAvailable runs after all clusters reach the Available condition.
// External OIDC setup runs here because the HC must be fully reconciled
// before the authentication config is patched: the HO needs to have
// created the HCP namespace and the HCCO must be running so that the
// issuer CA configmap and console client secret are propagated from the
// HC namespace → HCP namespace → guest openshift-config namespace
// before the console-operator deploys the console pod with OIDC auth.
func (a *AzurePlatformConfig) PostAvailable(ctx context.Context, cl crclient.WithWatch, namespace string, clusterNames map[string]string) error {
	return nil
}

func (a *AzurePlatformConfig) PostVersionRollout(ctx context.Context, cl crclient.WithWatch, namespace string, clusterNames map[string]string) error {
	if oidcName, ok := clusterNames["cluster-name-external-oidc"]; ok {
		if err := a.postCreateExternalOIDC(ctx, cl, namespace, oidcName); err != nil {
			return err
		}
	}
	return nil
}

func (a *AzurePlatformConfig) postCreatePublic(ctx context.Context, cl crclient.Client, namespace, name string) error {
	hc := &hyperv1.HostedCluster{}
	if err := cl.Get(ctx, crclient.ObjectKey{Namespace: namespace, Name: name}, hc); err != nil {
		return fmt.Errorf("getting HostedCluster %s/%s: %w", namespace, name, err)
	}

	patch := crclient.MergeFrom(hc.DeepCopy())
	if hc.Spec.OperatorConfiguration == nil {
		hc.Spec.OperatorConfiguration = &hyperv1.OperatorConfiguration{}
	}
	hc.Spec.OperatorConfiguration.IngressOperator = &hyperv1.IngressOperatorSpec{
		EndpointPublishingStrategy: &operatorv1.EndpointPublishingStrategy{
			Type: operatorv1.LoadBalancerServiceStrategyType,
			LoadBalancer: &operatorv1.LoadBalancerStrategy{
				Scope: operatorv1.InternalLoadBalancer,
			},
		},
	}
	if err := cl.Patch(ctx, hc, patch); err != nil {
		return fmt.Errorf("patching HostedCluster %s/%s OperatorConfiguration: %w", namespace, name, err)
	}
	log.Printf("Patched public cluster %s/%s with OperatorConfiguration", namespace, name)
	return nil
}

func (a *AzurePlatformConfig) postCreateExternalOIDC(ctx context.Context, cl crclient.Client, namespace, name string) error {
	hc := &hyperv1.HostedCluster{}
	if err := cl.Get(ctx, crclient.ObjectKey{Namespace: namespace, Name: name}, hc); err != nil {
		return fmt.Errorf("getting HostedCluster %s/%s for OIDC setup: %w", namespace, name, err)
	}

	kcConfig := a.keycloakConfig
	if kcConfig == nil {
		return fmt.Errorf("keycloak config not available; PreCreate must run before PostCreate")
	}

	consoleRedirectURI := fmt.Sprintf("https://console-openshift-console.apps.%s.%s/auth/callback",
		hc.Name, hc.Spec.DNS.BaseDomain)
	if err := v2util.UpdateKeycloakConsoleClient(ctx, consoleRedirectURI); err != nil {
		return fmt.Errorf("updating keycloak console client redirect URI: %w", err)
	}

	caCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "oidc-ca",
			Namespace: namespace,
		},
		Data: map[string]string{
			"ca-bundle.crt": string(kcConfig.CABundle),
		},
	}
	if err := v2util.CreateOrUpdate(ctx, cl, caCM); err != nil {
		return fmt.Errorf("creating OIDC CA configmap: %w", err)
	}

	consoleSecret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "console-secret",
			Namespace: namespace,
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"clientSecret": kcConfig.ConsoleClientSecret,
		},
	}
	if err := v2util.CreateOrUpdate(ctx, cl, consoleSecret); err != nil {
		return fmt.Errorf("creating console client secret: %w", err)
	}

	extOIDCConfig := &e2eutil.ExtOIDCConfig{
		ExternalOIDCProvider:     e2eutil.ProviderKeycloak,
		OIDCProviderName:         "keycloak oidc server",
		CliClientID:              kcConfig.CLIClientID,
		ConsoleClientID:          kcConfig.ConsoleClientID,
		IssuerURL:                kcConfig.IssuerURL,
		GroupPrefix:              "oidc-groups-test:",
		UserPrefix:               "oidc-user-test:",
		ConsoleClientSecretName:  "console-secret",
		ConsoleClientSecretValue: kcConfig.ConsoleClientSecret,
		IssuerCAConfigmapName:    "oidc-ca",
		TestUsers:                kcConfig.TestUsers,
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

	if a.sharedDir != "" {
		caPath := filepath.Join(a.sharedDir, "external_oidc_ca_bundle")
		if err := os.WriteFile(caPath, kcConfig.CABundle, 0600); err != nil {
			return fmt.Errorf("writing CA bundle to %s: %w", caPath, err)
		}
		testUsersPath := filepath.Join(a.sharedDir, "external_oidc_test_users")
		if err := os.WriteFile(testUsersPath, []byte(kcConfig.TestUsers), 0600); err != nil {
			return fmt.Errorf("writing test users to %s: %w", testUsersPath, err)
		}
		log.Printf("Wrote External OIDC CA bundle and test users to SHARED_DIR")
	}

	return nil
}

func (a *AzurePlatformConfig) TestMatrix(releaseImage string) TestMatrix {
	return TestMatrix{
		Parallel: []TestGroup{
			{
				Name:        "public",
				ClusterFile: "cluster-name-public",
				LabelFilter: "self-managed-azure-public || nodepool-lifecycle || secret-encryption || control-plane-workloads || hosted-cluster-security",
				Skip:        "KAS allowed CIDRs",
				JUnitFile:   "junit_self_managed_azure_public.xml",
			},
			{
				Name:        "private",
				ClusterFile: "cluster-name-private",
				LabelFilter: "self-managed-azure-private || self-managed-azure-oauth-lb-private || hosted-cluster-compliance",
				JUnitFile:   "junit_self_managed_azure_private.xml",
			},
			{
				Name:        "oauth-lb",
				ClusterFile: "cluster-name-oauth-lb",
				LabelFilter: "self-managed-azure-oauth-lb || hosted-cluster-health || hosted-cluster-metrics || hosted-cluster-image-registry",
				JUnitFile:   "junit_self_managed_azure_oauth_lb.xml",
			},
			{
				Name:        "autoscaling",
				ClusterFile: "cluster-name-autoscaling",
				LabelFilter: "nodepool-autoscaling",
				JUnitFile:   "junit_nodepool_autoscaling.xml",
			},
			{
				Name:        "external-oidc",
				ClusterFile: "cluster-name-external-oidc",
				LabelFilter: "external-oidc",
				JUnitFile:   "junit_self_managed_azure_external_oidc.xml",
			},
		},
		Sequential: []SequentialGroup{
			{
				Name: "upgrade-and-chaos",
				Steps: []TestGroup{
					{
						Name:        "upgrade",
						ClusterFile: "cluster-name-upgrade",
						LabelFilter: "control-plane-upgrade",
						JUnitFile:   "junit_lifecycle_upgrade.xml",
						ExtraEnv:    []string{fmt.Sprintf("E2E_LATEST_RELEASE_IMAGE=%s", releaseImage)},
					},
					{
						Name:        "etcd-chaos",
						ClusterFile: "cluster-name-upgrade",
						LabelFilter: "etcd-chaos",
						JUnitFile:   "junit_lifecycle_etcd_chaos.xml",
					},
				},
			},
		},
	}
}

// SetupTestEnv reads Azure-specific config from SHARED_DIR and sets
// environment variables for the test subprocesses.
func (a *AzurePlatformConfig) SetupTestEnv(sharedDir string) {
	azurePrivateNATSubnetID := os.Getenv("AZURE_PRIVATE_NAT_SUBNET_ID")
	if data, err := os.ReadFile(filepath.Join(sharedDir, "azure_private_nat_subnet_id")); err == nil {
		azurePrivateNATSubnetID = strings.TrimSpace(string(data))
	}
	os.Setenv("AZURE_PRIVATE_NAT_SUBNET_ID", azurePrivateNATSubnetID)

	// External OIDC
	caPath := filepath.Join(sharedDir, "external_oidc_ca_bundle")
	if _, err := os.Stat(caPath); err == nil {
		os.Setenv("E2E_EXTERNAL_OIDC_CA_BUNDLE_FILE", caPath)
	}
	if data, err := os.ReadFile(filepath.Join(sharedDir, "external_oidc_test_users")); err == nil {
		os.Setenv("E2E_EXTERNAL_OIDC_TEST_USERS", strings.TrimSpace(string(data)))
	}
}

func (a *AzurePlatformConfig) DestroyArgs() []string {
	return []string{
		"--azure-creds=" + a.creds,
		"--location=" + a.location,
		"--dns-zone-rg-name=" + a.dnsZoneRG,
	}
}

func envOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}
