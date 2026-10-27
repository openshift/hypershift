//go:build e2ev2

package lifecycle

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	defaultGCPRegion         = "us-central1"
	defaultGCPEndpointAccess = "Private"
	defaultGCPBaseDomain     = "hypershift-gcp.ci.devcluster.openshift.com"
)

// GCPPlatformConfig holds GCP-specific configuration for the
// hypershift CLI.
type GCPPlatformConfig struct {
	project                    string
	region                     string
	network                    string
	subnet                     string
	pscSubnet                  string
	endpointAccess             string
	oidcIssuerURL              string
	saTokenKeyPath             string
	wifProjectNumber           string
	wifPoolID                  string
	wifProviderID              string
	controlPlaneServiceAccount string
	nodePoolServiceAccount     string
	cloudControllerSA          string
	storageSA                  string
	imageRegistrySA            string
	networkSA                  string
	sharedDir                  string
}

// NewGCPPlatformConfig reads GCP-specific configuration from
// environment variables with CI defaults.
func NewGCPPlatformConfig(sharedDir string) *GCPPlatformConfig {
	cfg := &GCPPlatformConfig{
		project:                    os.Getenv("GCP_PROJECT_ID"),
		region:                     envOrDefault("HYPERSHIFT_GCP_REGION", defaultGCPRegion),
		network:                    os.Getenv("GCP_NETWORK"),
		subnet:                     os.Getenv("GCP_SUBNET"),
		pscSubnet:                  os.Getenv("GCP_PSC_SUBNET"),
		endpointAccess:             envOrDefault("GCP_ENDPOINT_ACCESS", defaultGCPEndpointAccess),
		oidcIssuerURL:              os.Getenv("GCP_OIDC_ISSUER_URL"),
		saTokenKeyPath:             os.Getenv("GCP_SA_SIGNING_KEY_PATH"),
		wifProjectNumber:           os.Getenv("GCP_WIF_PROJECT_NUMBER"),
		wifPoolID:                  os.Getenv("GCP_WIF_POOL_ID"),
		wifProviderID:              os.Getenv("GCP_WIF_PROVIDER_ID"),
		controlPlaneServiceAccount: os.Getenv("GCP_CONTROL_PLANE_SA"),
		nodePoolServiceAccount:     os.Getenv("GCP_NODE_POOL_SA"),
		cloudControllerSA:          os.Getenv("GCP_CLOUD_CONTROLLER_SA"),
		storageSA:                  os.Getenv("GCP_STORAGE_SA"),
		imageRegistrySA:            os.Getenv("GCP_IMAGE_REGISTRY_SA"),
		networkSA:                  os.Getenv("GCP_NETWORK_SA"),
		sharedDir:                  sharedDir,
	}

	// Fall back to SHARED_DIR files when env vars are not set.
	if cfg.pscSubnet == "" && cfg.subnet != "" {
		cfg.pscSubnet = cfg.subnet
	}
	if cfg.saTokenKeyPath == "" && sharedDir != "" {
		candidate := filepath.Join(sharedDir, "sa-signer.key")
		if _, err := os.Stat(candidate); err == nil {
			cfg.saTokenKeyPath = candidate
		}
	}

	log.Printf("GCP platform config: project=%s, region=%s, network=%s, endpoint=%s",
		cfg.project, cfg.region, cfg.network, cfg.endpointAccess)
	return cfg
}

func (g *GCPPlatformConfig) Name() string { return "gcp" }

func (g *GCPPlatformConfig) DefaultBaseDomain() string {
	return defaultGCPBaseDomain
}

func (g *GCPPlatformConfig) ClusterSpecs(releaseImage, n1Image string) []ClusterSpec {
	var extraArgs []string
	if envArgs := os.Getenv("EXTRA_ARGS"); envArgs != "" {
		extraArgs = strings.Fields(envArgs)
	}
	return []ClusterSpec{
		{
			Variant:   "public",
			ExtraArgs: extraArgs,
		},
	}
}

func (g *GCPPlatformConfig) CreateArgs() []string {
	args := []string{
		"--pods-labels=hypershift-e2e-test-label=test",
		"--toleration=key=hypershift-e2e-test-toleration,operator=Equal,value=true,effect=NoSchedule",
		"--annotations=hypershift.openshift.io/skip-release-image-validation=true",
		"--annotations=hypershift.openshift.io/pod-security-admission-label-override=baseline",
		"--feature-set=TechPreviewNoUpgrade",
	}

	if g.project != "" {
		args = append(args, "--project="+g.project)
	}
	if g.region != "" {
		args = append(args, "--region="+g.region)
	}
	if g.network != "" {
		args = append(args, "--network="+g.network)
	}
	if g.pscSubnet != "" {
		args = append(args, "--private-service-connect-subnet="+g.pscSubnet)
	}
	if g.endpointAccess != "" {
		args = append(args, "--endpoint-access="+g.endpointAccess)
	}
	if g.oidcIssuerURL != "" {
		args = append(args, "--oidc-issuer-url="+g.oidcIssuerURL)
	}
	if g.saTokenKeyPath != "" {
		args = append(args, "--service-account-signing-key-path="+g.saTokenKeyPath)
	}
	if g.wifProjectNumber != "" {
		args = append(args, "--workload-identity-project-number="+g.wifProjectNumber)
	}
	if g.wifPoolID != "" {
		args = append(args, "--workload-identity-pool-id="+g.wifPoolID)
	}
	if g.wifProviderID != "" {
		args = append(args, "--workload-identity-provider-id="+g.wifProviderID)
	}
	if g.controlPlaneServiceAccount != "" {
		args = append(args, "--control-plane-service-account="+g.controlPlaneServiceAccount)
	}
	if g.nodePoolServiceAccount != "" {
		args = append(args, "--node-pool-service-account="+g.nodePoolServiceAccount)
	}
	if g.cloudControllerSA != "" {
		args = append(args, "--cloud-controller-service-account="+g.cloudControllerSA)
	}
	if g.storageSA != "" {
		args = append(args, "--storage-service-account="+g.storageSA)
	}
	if g.imageRegistrySA != "" {
		args = append(args, "--image-registry-service-account="+g.imageRegistrySA)
	}
	if g.networkSA != "" {
		args = append(args, "--network-service-account="+g.networkSA)
	}

	return args
}

func (g *GCPPlatformConfig) PreCreate(ctx context.Context, cl crclient.WithWatch, namespace string) error {
	return nil
}

func (g *GCPPlatformConfig) PostCreate(ctx context.Context, cl crclient.WithWatch, namespace string, clusterNames map[string]string) error {
	return nil
}

func (g *GCPPlatformConfig) PostAvailable(ctx context.Context, cl crclient.WithWatch, namespace string, clusterNames map[string]string) error {
	return nil
}

func (g *GCPPlatformConfig) PostVersionRollout(ctx context.Context, cl crclient.WithWatch, namespace string, clusterNames map[string]string) error {
	return nil
}

func (g *GCPPlatformConfig) TestMatrix(releaseImage string) TestMatrix {
	return TestMatrix{
		Parallel: []TestGroup{
			{
				Name:        "gcp-public",
				Variant:     "public",
				LabelFilter: "!lifecycle || control-plane-workloads",
				JUnitFile:   "junit_gcp_public.xml",
			},
		},
	}
}

func (g *GCPPlatformConfig) SetupTestEnv(sharedDir string) {}

func (g *GCPPlatformConfig) DestroyArgs() []string {
	return []string{}
}
