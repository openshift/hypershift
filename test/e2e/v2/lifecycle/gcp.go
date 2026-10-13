//go:build e2ev2

package lifecycle

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// defaultGCPBaseDomain is the shared CI DNS domain used as the
	// HostedCluster base domain. Per-cluster DNS zones are provisioned by
	// the control-plane-operator; API/OAuth records are handled via
	// --external-dns-domain.
	defaultGCPBaseDomain = "hypershift-ci.gcp-hcp.openshiftapps.com"

	// defaultGCPBootImage pins the RHCOS boot image for NodePool nodes.
	// TODO(GCP-440): Remove once the NodePool controller discovers the boot
	// image from stream metadata.
	defaultGCPBootImage = "projects/rhcos-cloud/global/images/rhcos-9-6-20250925-0-gcp-x86-64"
)

// gcpVariantInfra holds the GCP infrastructure values a single cluster
// variant needs on the "create cluster gcp" command line. In this
// (account-level) iteration every variant shares the same infra, read
// from SHARED_DIR; a later change will provision dedicated infra per
// variant in PreCreate and populate one of these per variant.
type gcpVariantInfra struct {
	network           string
	pscSubnet         string
	wifProjectNumber  string
	wifPoolID         string
	wifProviderID     string
	controlPlaneSA    string
	nodePoolSA        string
	cloudControllerSA string
	storageSA         string
	imageRegistrySA   string
	networkSA         string
	saSigningKeyPath  string
	oidcIssuerURL     string
}

// GCPPlatformConfig holds GCP-specific configuration for the hypershift
// CLI. Account-level invariants (project, region, zone, DNS domain, boot
// image) are read from the environment / SHARED_DIR at construction;
// per-variant infrastructure flags are resolved in PreCreate.
type GCPPlatformConfig struct {
	sharedDir string

	project     string
	region      string
	zone        string
	ciDNSDomain string
	bootImage   string

	// variantInfra maps a cluster variant to its resolved create flags,
	// populated by PreCreate and consumed by CreateArgsForVariant.
	variantInfra map[string]gcpVariantInfra
}

// NewGCPPlatformConfig reads GCP account-level configuration from
// environment variables and SHARED_DIR files written by the CI provision
// steps, applying CI defaults.
func NewGCPPlatformConfig(sharedDir string) *GCPPlatformConfig {
	cfg := &GCPPlatformConfig{
		sharedDir:    sharedDir,
		zone:         os.Getenv("HYPERSHIFT_GCP_ZONE"),
		ciDNSDomain:  os.Getenv("HYPERSHIFT_GCP_CI_DNS_DOMAIN"),
		bootImage:    envOrDefault("HYPERSHIFT_GCP_BOOT_IMAGE", defaultGCPBootImage),
		variantInfra: map[string]gcpVariantInfra{},
	}

	cfg.project = os.Getenv("HYPERSHIFT_GCP_PROJECT")
	if cfg.project == "" {
		cfg.project = readSharedFile(sharedDir, "hosted-cluster-project-id")
	}
	cfg.region = os.Getenv("HYPERSHIFT_GCP_REGION")
	if cfg.region == "" {
		cfg.region = readSharedFile(sharedDir, "gcp-region")
	}

	log.Printf("GCP platform config: project=%s, region=%s, zone=%s, ciDNSDomain=%s",
		cfg.project, cfg.region, cfg.zone, cfg.ciDNSDomain)
	return cfg
}

func (g *GCPPlatformConfig) Name() string { return "gcp" }

func (g *GCPPlatformConfig) DefaultBaseDomain() string {
	if g.ciDNSDomain != "" {
		return g.ciDNSDomain
	}
	return defaultGCPBaseDomain
}

// ClusterSpecs returns the GCP cluster variants to create. Currently only
// the "default" variant (Console + Ingress disabled, TechPreviewNoUpgrade),
// reproducing today's single-cluster e2e-v2-gke behavior. Additional
// variants are appended here and require no change outside this file.
func (g *GCPPlatformConfig) ClusterSpecs(releaseImage, n1Image string) []ClusterSpec {
	var extraArgs []string
	if envArgs := os.Getenv("EXTRA_ARGS"); envArgs != "" {
		extraArgs = strings.Fields(envArgs)
	}

	return []ClusterSpec{
		{
			Variant: "default",
			ExtraArgs: append([]string{
				"--disable-cluster-capabilities=Console",
				"--disable-cluster-capabilities=Ingress",
				"--feature-set=TechPreviewNoUpgrade",
			}, extraArgs...),
		},
	}
}

// CreateArgs returns the account-level (variant-independent) flags shared
// by every GCP cluster.
func (g *GCPPlatformConfig) CreateArgs() []string {
	args := []string{
		"--project=" + g.project,
		"--region=" + g.region,
		"--endpoint-access=PublicAndPrivate",
		"--annotations=hypershift.openshift.io/pod-security-admission-label-override=baseline",
	}
	if g.zone != "" {
		args = append(args, "--zone="+g.zone)
	}
	if g.ciDNSDomain != "" {
		args = append(args, "--external-dns-domain="+g.ciDNSDomain)
	}
	if g.bootImage != "" {
		args = append(args, "--boot-image="+g.bootImage)
	}
	return args
}

// CreateArgsForVariant returns the per-variant infrastructure flags
// resolved during PreCreate. It returns nil for a variant with no
// resolved infra (PreCreate must run first).
func (g *GCPPlatformConfig) CreateArgsForVariant(variant string) []string {
	infra, ok := g.variantInfra[variant]
	if !ok {
		return nil
	}
	return variantCreateArgs(infra)
}

// variantCreateArgs renders a gcpVariantInfra as "create cluster gcp"
// flags. It is pure so arg construction is testable without GCP access.
func variantCreateArgs(i gcpVariantInfra) []string {
	return []string{
		"--network=" + i.network,
		"--private-service-connect-subnet=" + i.pscSubnet,
		"--workload-identity-project-number=" + i.wifProjectNumber,
		"--workload-identity-pool-id=" + i.wifPoolID,
		"--workload-identity-provider-id=" + i.wifProviderID,
		"--control-plane-service-account=" + i.controlPlaneSA,
		"--node-pool-service-account=" + i.nodePoolSA,
		"--cloud-controller-service-account=" + i.cloudControllerSA,
		"--storage-service-account=" + i.storageSA,
		"--image-registry-service-account=" + i.imageRegistrySA,
		"--network-service-account=" + i.networkSA,
		"--service-account-signing-key-path=" + i.saSigningKeyPath,
		"--oidc-issuer-url=" + i.oidcIssuerURL,
	}
}

// PreCreate resolves per-variant GCP infrastructure flags. In this
// iteration the WIF pool/provider, service accounts and network are
// provisioned account-wide by the CI setup step and shared by every
// variant; PreCreate reads those values from SHARED_DIR and assigns the
// same resolved infra to each variant. A later change will instead
// provision dedicated infra per variant here.
func (g *GCPPlatformConfig) PreCreate(ctx context.Context, cl crclient.WithWatch, namespace string) error {
	infra, err := g.readSharedInfra()
	if err != nil {
		return err
	}
	for _, spec := range g.ClusterSpecs("", "") {
		g.variantInfra[spec.Variant] = infra
	}
	return nil
}

// readSharedInfra reads the account-level GCP infrastructure values that
// the CI setup step writes to SHARED_DIR and returns them as a
// gcpVariantInfra. It errors if any required file is missing or empty.
func (g *GCPPlatformConfig) readSharedInfra() (gcpVariantInfra, error) {
	required := map[string]*string{}
	infra := gcpVariantInfra{}
	required["hc-vpc-name"] = &infra.network
	required["hc-subnet-name"] = &infra.pscSubnet
	required["wif-project-number"] = &infra.wifProjectNumber
	required["wif-pool-id"] = &infra.wifPoolID
	required["wif-provider-id"] = &infra.wifProviderID
	required["controlplane-sa"] = &infra.controlPlaneSA
	required["nodepool-sa"] = &infra.nodePoolSA
	required["cloudcontroller-sa"] = &infra.cloudControllerSA
	required["storage-sa"] = &infra.storageSA
	required["imageregistry-sa"] = &infra.imageRegistrySA
	required["network-sa"] = &infra.networkSA
	required["sa-signing-key-path"] = &infra.saSigningKeyPath

	var missing []string
	for name, dst := range required {
		val := readSharedFile(g.sharedDir, name)
		if val == "" {
			missing = append(missing, name)
			continue
		}
		*dst = val
	}
	if len(missing) > 0 {
		return gcpVariantInfra{}, fmt.Errorf("missing GCP infra values in SHARED_DIR: %v", missing)
	}

	// The WIF provider trusts the OIDC issuer bound to the CI cluster name
	// at provisioning time; derive the issuer URL from that same name.
	clusterName := readSharedFile(g.sharedDir, "hosted-cluster-name")
	if clusterName == "" {
		return gcpVariantInfra{}, fmt.Errorf("missing GCP infra value in SHARED_DIR: hosted-cluster-name")
	}
	infra.oidcIssuerURL = fmt.Sprintf("https://hypershift-%s-oidc", clusterName)

	return infra, nil
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

func (g *GCPPlatformConfig) DefaultTestPlan() TestPlan {
	return TestPlan{
		Name:       "gcp-full",
		Platform:   "gcp",
		TestMatrix: g.TestMatrix(),
	}
}

// TestMatrix runs the GCP suite with today's exact label filter: all
// non-lifecycle specs plus the one GCP-specific lifecycle spec.
func (g *GCPPlatformConfig) TestMatrix() TestMatrix {
	return TestMatrix{
		Parallel: []TestGroup{
			{
				Name:        "default",
				Variant:     "default",
				LabelFilter: "!lifecycle || gcp-worker-firewall",
			},
		},
	}
}

// SetupTestEnv sets GCP-specific environment variables for the test
// subprocesses. The current GCP suite reads no SHARED_DIR-sourced env, so
// this is a no-op; add exports here as GCP test coverage grows.
func (g *GCPPlatformConfig) SetupTestEnv(sharedDir string) {}

// DestroyArgs returns no platform-specific flags: "destroy cluster gcp"
// only removes the HostedCluster resource; GCP infra is reaped by the CI
// project teardown.
func (g *GCPPlatformConfig) DestroyArgs() []string { return nil }

// readSharedFile returns the trimmed contents of sharedDir/name, or ""
// if sharedDir is empty or the file cannot be read.
func readSharedFile(sharedDir, name string) string {
	if sharedDir == "" {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(sharedDir, name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
