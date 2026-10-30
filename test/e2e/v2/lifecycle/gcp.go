//go:build e2ev2

package lifecycle

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	// defaultGCPBaseDomain is the shared CI DNS domain, used as the
	// fallback base domain and as the external-dns domain under which
	// API/OAuth records (api-<name>/<oauth-name> prefixed per cluster) are
	// published. The HostedCluster base domain itself must be unique per
	// cluster: the control-plane-operator names its public and private DNS
	// zones exactly after the base domain, so a shared one would collide
	// across clusters. ClusterSpecs therefore derives a per-cluster domain
	// (in.<cluster-name>.<domain>) instead.
	defaultGCPBaseDomain = "hypershift-ci.gcp-hcp.openshiftapps.com"

	// maxDNSHostnameLength is the DNS protocol limit for a hostname. A base
	// domain at or near it leaves no room for the api-/oauth- record labels,
	// so cluster creation would fail deep in provisioning with an unhelpful
	// error; checkBaseDomainLength fails fast instead.
	maxDNSHostnameLength = 253
)

// gcpInfra holds the GCP infrastructure values the "create cluster gcp"
// command line needs. In this (account-level) iteration every variant
// shares the same infra, read from SHARED_DIR at construction; a later
// change will provision dedicated infra per variant.
type gcpInfra struct {
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
// image) are read from the environment, and the shared infrastructure
// flags from SHARED_DIR, at construction so that ClusterSpecs can express
// the full command line declaratively.
type GCPPlatformConfig struct {
	project     string
	region      string
	zone        string
	ciDNSDomain string
	bootImage   string

	// prowJobID identifies the CI run; per-cluster base domains are
	// derived from the cluster names it generates.
	prowJobID string

	infra gcpInfra
}

// NewGCPPlatformConfig reads GCP configuration from environment variables
// and the SHARED_DIR files written by the CI provision steps, applying CI
// defaults. It errors if any required SHARED_DIR infra value is missing so
// create-guests fails fast with a clear message.
func NewGCPPlatformConfig(sharedDir string) (*GCPPlatformConfig, error) {
	cfg := &GCPPlatformConfig{
		zone:        os.Getenv("HYPERSHIFT_GCP_ZONE"),
		ciDNSDomain: os.Getenv("HYPERSHIFT_GCP_CI_DNS_DOMAIN"),
		bootImage:   os.Getenv("HYPERSHIFT_GCP_BOOT_IMAGE"),
		prowJobID:   os.Getenv("PROW_JOB_ID"),
	}

	cfg.project = os.Getenv("HYPERSHIFT_GCP_PROJECT")
	if cfg.project == "" {
		project, err := readSharedFile(sharedDir, "hosted-cluster-project-id")
		if err != nil {
			log.Printf("WARNING: reading hosted-cluster-project-id from SHARED_DIR: %v", err)
		}
		cfg.project = project
	}
	cfg.region = os.Getenv("HYPERSHIFT_GCP_REGION")
	if cfg.region == "" {
		region, err := readSharedFile(sharedDir, "gcp-region")
		if err != nil {
			log.Printf("WARNING: reading gcp-region from SHARED_DIR: %v", err)
		}
		cfg.region = region
	}

	infra, err := readGCPInfra(sharedDir)
	if err != nil {
		return nil, err
	}
	cfg.infra = infra

	log.Printf("GCP platform config: project=%s, region=%s, zone=%s, ciDNSDomain=%s",
		cfg.project, cfg.region, cfg.zone, cfg.ciDNSDomain)
	return cfg, nil
}

func (g *GCPPlatformConfig) Name() string { return "gcp" }

// DefaultBaseDomain returns the shared CI DNS domain. It is only the
// fallback for the global --base-domain flag; each cluster's effective
// base domain is the per-cluster --base-domain emitted by ClusterSpecs.
func (g *GCPPlatformConfig) DefaultBaseDomain() string {
	if g.ciDNSDomain != "" {
		return g.ciDNSDomain
	}
	return defaultGCPBaseDomain
}

// ClusterSpecs returns the GCP cluster variants to create, expressing the
// complete "create cluster gcp" command line per variant. Currently only
// the "default" variant (Console + Ingress disabled,
// TechPreviewNoUpgrade), reproducing today's single-cluster e2e-v2-gke
// behavior. Additional variants are appended here and require no change
// outside this file.
func (g *GCPPlatformConfig) ClusterSpecs(_ context.Context, releaseImage, n1Image string) []ClusterSpec {
	commonArgs := []string{
		"--project=" + g.project,
		"--region=" + g.region,
		"--endpoint-access=PublicAndPrivate",
		"--annotations=hypershift.openshift.io/pod-security-admission-label-override=baseline",
	}
	if g.zone != "" {
		commonArgs = append(commonArgs, "--zone="+g.zone)
	}
	if g.ciDNSDomain != "" {
		commonArgs = append(commonArgs, "--external-dns-domain="+g.ciDNSDomain)
	}
	if g.bootImage != "" {
		commonArgs = append(commonArgs, "--boot-image="+g.bootImage)
	}
	commonArgs = append(commonArgs, g.infra.args()...)

	var extraArgs []string
	if envArgs := os.Getenv("EXTRA_ARGS"); envArgs != "" {
		extraArgs = strings.Fields(envArgs)
	}

	return []ClusterSpec{
		{
			Variant: "default",
			CreateArgs: slices.Concat(
				// The per-cluster --base-domain overrides the global one
				// (pflag keeps the last value).
				[]string{"--base-domain=" + g.variantBaseDomain("default")},
				[]string{
					"--disable-cluster-capabilities=Console",
					"--disable-cluster-capabilities=Ingress",
					"--feature-set=TechPreviewNoUpgrade",
				},
				commonArgs,
				extraArgs,
			),
		},
	}
}

// PreCreate validates the per-cluster configuration. Create flags are
// expressed declaratively by ClusterSpecs, which cannot return errors, so
// constraints on generated values are checked here where failures surface
// clearly. A later change will provision dedicated infra per variant.
func (g *GCPPlatformConfig) PreCreate(ctx context.Context, cl crclient.WithWatch, namespace string) error {
	for _, spec := range g.ClusterSpecs(ctx, "", "") {
		if err := checkBaseDomainLength(spec.Variant, g.variantBaseDomain(spec.Variant)); err != nil {
			return err
		}
	}
	return nil
}

// variantBaseDomain returns a base domain unique to the cluster created
// for the given variant. The control-plane-operator creates public and
// private DNS zones named exactly after the HostedCluster base domain, so
// a shared base domain would collide across clusters and the per-cluster
// private zone would shadow resolution of the shared external-dns records.
// Deriving the domain from the cluster name mirrors the
// in.<cluster-name>.<domain> scheme of the single-cluster CI chain.
func (g *GCPPlatformConfig) variantBaseDomain(variant string) string {
	return "in." + DeriveClusterName(g.prowJobID, variant) + "." + g.DefaultBaseDomain()
}

// args renders the shared infrastructure values as "create cluster gcp"
// flags. It is pure so arg construction is testable without GCP access.
func (i gcpInfra) args() []string {
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

// readGCPInfra reads the account-level GCP infrastructure values that the
// CI setup step writes to SHARED_DIR. It errors if any required file is
// missing or empty.
func readGCPInfra(sharedDir string) (gcpInfra, error) {
	required := map[string]*string{}
	infra := gcpInfra{}
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
		val, err := readSharedFile(sharedDir, name)
		if err != nil {
			return gcpInfra{}, fmt.Errorf("reading GCP infra value %s from SHARED_DIR: %w", name, err)
		}
		if val == "" {
			missing = append(missing, name)
			continue
		}
		*dst = val
	}
	if len(missing) > 0 {
		slices.Sort(missing)
		return gcpInfra{}, fmt.Errorf("missing GCP infra values in SHARED_DIR: %v", missing)
	}

	// The WIF provider trusts the OIDC issuer bound to the CI cluster name
	// at provisioning time; derive the issuer URL from that same name.
	clusterName, err := readSharedFile(sharedDir, "hosted-cluster-name")
	if err != nil {
		return gcpInfra{}, fmt.Errorf("reading GCP infra value hosted-cluster-name from SHARED_DIR: %w", err)
	}
	if clusterName == "" {
		return gcpInfra{}, fmt.Errorf("missing GCP infra value in SHARED_DIR: hosted-cluster-name")
	}
	infra.oidcIssuerURL = fmt.Sprintf("https://hypershift-%s-oidc", clusterName)

	return infra, nil
}

// checkBaseDomainLength rejects a generated base domain that exceeds the
// DNS hostname limit. It is pure so the check is testable without GCP
// access. Failing here means the CI DNS domain (or variant names) must be
// shortened, not that the code should truncate the domain.
func checkBaseDomainLength(variant, domain string) error {
	if len(domain) > maxDNSHostnameLength {
		return fmt.Errorf("base domain for variant %q is %d characters, exceeding the %d-character DNS hostname limit (use a shorter CI DNS domain): %s",
			variant, len(domain), maxDNSHostnameLength, domain)
	}
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

// readSharedFile returns the trimmed contents of sharedDir/name. A missing
// file (or an empty sharedDir) yields "", nil; any other read error is
// returned so callers can distinguish "not written yet" from real I/O
// problems.
func readSharedFile(sharedDir, name string) (string, error) {
	if sharedDir == "" {
		return "", nil
	}
	data, err := os.ReadFile(filepath.Join(sharedDir, name))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}
