//go:build e2ev2

package lifecycle

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// testGCPInfra returns a fully populated gcpInfra for asserting the flags
// ClusterSpecs derives from it.
func testGCPInfra() gcpInfra {
	return gcpInfra{
		network:           "vpc-net",
		pscSubnet:         "psc-subnet",
		wifProjectNumber:  "123456",
		wifPoolID:         "pool",
		wifProviderID:     "provider",
		controlPlaneSA:    "cp@sa",
		nodePoolSA:        "np@sa",
		cloudControllerSA: "cc@sa",
		storageSA:         "storage@sa",
		imageRegistrySA:   "ir@sa",
		networkSA:         "net@sa",
		saSigningKeyPath:  "/shared/sa-signer.key",
		oidcIssuerURL:     "https://hypershift-ci123-oidc",
	}
}

// writeSharedInfra writes a complete set of GCP SHARED_DIR infra files and
// returns the directory.
func writeSharedInfra(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"hc-vpc-name":               "vpc-net",
		"hc-subnet-name":            "psc-subnet",
		"wif-project-number":        "123456",
		"wif-pool-id":               "pool",
		"wif-provider-id":           "provider",
		"controlplane-sa":           "cp@sa",
		"nodepool-sa":               "np@sa",
		"cloudcontroller-sa":        "cc@sa",
		"storage-sa":                "storage@sa",
		"imageregistry-sa":          "ir@sa",
		"network-sa":                "net@sa",
		"sa-signing-key-path":       "/shared/sa-signer.key",
		"hosted-cluster-name":       "ci123",
		"hosted-cluster-project-id": "my-project",
		"gcp-region":                "us-central1",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestNewGCPPlatformConfig(t *testing.T) {
	t.Setenv("HYPERSHIFT_GCP_PROJECT", "")
	t.Setenv("HYPERSHIFT_GCP_REGION", "")
	t.Setenv("HYPERSHIFT_GCP_ZONE", "us-central1-a")
	t.Setenv("HYPERSHIFT_GCP_CI_DNS_DOMAIN", "example.gcp-hcp.test")
	t.Setenv("PROW_JOB_ID", "job-123")

	cfg, err := NewGCPPlatformConfig(writeSharedInfra(t))
	if err != nil {
		t.Fatalf("NewGCPPlatformConfig() = %v, want nil error", err)
	}

	// Project and region fall back to SHARED_DIR; the OIDC issuer is
	// derived from the CI cluster name the WIF provider trusts.
	if cfg.project != "my-project" || cfg.region != "us-central1" {
		t.Errorf("project/region = %q/%q, want my-project/us-central1", cfg.project, cfg.region)
	}
	if got, want := cfg.infra.oidcIssuerURL, "https://hypershift-ci123-oidc"; got != want {
		t.Errorf("oidcIssuerURL = %q, want %q", got, want)
	}
	wantInfra := testGCPInfra()
	wantInfra.oidcIssuerURL = "https://hypershift-ci123-oidc"
	if cfg.infra != wantInfra {
		t.Errorf("infra = %+v, want %+v", cfg.infra, wantInfra)
	}
}

func TestNewGCPPlatformConfigMissingInfra(t *testing.T) {
	t.Setenv("HYPERSHIFT_GCP_PROJECT", "")
	t.Setenv("HYPERSHIFT_GCP_REGION", "")

	// No SHARED_DIR at all: every required value is reported missing.
	_, err := NewGCPPlatformConfig("")
	if err == nil {
		t.Fatal("NewGCPPlatformConfig(empty sharedDir) = nil error, want error")
	}
	if !strings.Contains(err.Error(), "missing GCP infra values") {
		t.Errorf("error %q should report missing infra values", err)
	}

	// A single missing file is identified by name.
	dir := writeSharedInfra(t)
	if err := os.Remove(filepath.Join(dir, "wif-pool-id")); err != nil {
		t.Fatal(err)
	}
	_, err = NewGCPPlatformConfig(dir)
	if err == nil || !strings.Contains(err.Error(), "wif-pool-id") {
		t.Errorf("NewGCPPlatformConfig(missing wif-pool-id) error = %v, want it to name wif-pool-id", err)
	}
}

func TestGCPClusterSpecs(t *testing.T) {
	t.Parallel()

	cfg := &GCPPlatformConfig{
		project:     "my-project",
		region:      "us-central1",
		zone:        "us-central1-a",
		ciDNSDomain: "example.gcp-hcp.test",
		bootImage:   "projects/rhcos-cloud/global/images/boot",
		prowJobID:   "job-123",
		infra:       testGCPInfra(),
	}

	specs := cfg.ClusterSpecs(t.Context(), "release-image", "n1-image")
	if len(specs) != 1 {
		t.Fatalf("got %d GCP variants, want 1", len(specs))
	}
	spec := specs[0]
	if spec.Variant != "default" {
		t.Fatalf("GCP variant %q, want %q", spec.Variant, "default")
	}

	// Each flag encodes a distinct cluster-creation contract that a
	// regression could silently drop. The default variant must also
	// reproduce today's single-cluster e2e-v2-gke behavior exactly:
	// Console and Ingress disabled, TechPreviewNoUpgrade, and a
	// per-cluster base domain (see TestGCPVariantBaseDomain).
	wantArgs := append(testGCPInfra().args(), []string{
		"--project=my-project",
		"--region=us-central1",
		"--endpoint-access=PublicAndPrivate",
		"--annotations=hypershift.openshift.io/pod-security-admission-label-override=baseline",
		"--zone=us-central1-a",
		"--external-dns-domain=example.gcp-hcp.test",
		"--boot-image=projects/rhcos-cloud/global/images/boot",
		"--disable-cluster-capabilities=Console",
		"--disable-cluster-capabilities=Ingress",
		"--feature-set=TechPreviewNoUpgrade",
	}...)
	if !strings.HasPrefix(spec.CreateArgs[0], "--base-domain=in.default-") {
		t.Errorf("first CreateArg %q, want it to be the per-cluster base domain override", spec.CreateArgs[0])
	}
	for _, arg := range wantArgs {
		if !slices.Contains(spec.CreateArgs, arg) {
			t.Errorf("default CreateArgs %v missing %q", spec.CreateArgs, arg)
		}
	}
}

func TestGCPClusterSpecsHonorsExtraArgs(t *testing.T) {
	t.Setenv("EXTRA_ARGS", "--foo=bar --baz")

	specs := (&GCPPlatformConfig{}).ClusterSpecs(t.Context(), "release-image", "n1-image")
	if len(specs) != 1 {
		t.Fatalf("got %d GCP variants, want 1", len(specs))
	}
	for _, arg := range []string{"--foo=bar", "--baz"} {
		if !slices.Contains(specs[0].CreateArgs, arg) {
			t.Errorf("default CreateArgs %v missing EXTRA_ARGS entry %q", specs[0].CreateArgs, arg)
		}
	}
}

func TestGCPTestMatrixValidates(t *testing.T) {
	t.Parallel()

	cfg := &GCPPlatformConfig{}
	if err := cfg.TestMatrix().Validate(); err != nil {
		t.Fatalf("GCP TestMatrix().Validate() = %v, want nil", err)
	}

	// Every variant referenced by the matrix must be produced by ClusterSpecs.
	plan := cfg.DefaultTestPlan()
	if err := plan.Validate(cfg.ClusterSpecs(t.Context(), "release-image", "n1-image")); err != nil {
		t.Fatalf("GCP DefaultTestPlan().Validate() = %v, want nil", err)
	}

	groups := cfg.TestMatrix().Parallel
	if len(groups) != 1 {
		t.Fatalf("got %d parallel groups, want 1", len(groups))
	}
	if got, want := groups[0].LabelFilter, "!lifecycle || gcp-worker-firewall"; got != want {
		t.Errorf("default label filter = %q, want %q", got, want)
	}
}

func TestGCPVariantBaseDomain(t *testing.T) {
	t.Parallel()

	cfg := &GCPPlatformConfig{
		prowJobID:   "job-123",
		ciDNSDomain: "example.gcp-hcp.test",
	}

	got := cfg.variantBaseDomain("default")
	if !strings.HasPrefix(got, "in.default-") || !strings.HasSuffix(got, ".example.gcp-hcp.test") {
		t.Errorf("variantBaseDomain(default) = %q, want in.default-<hash>.example.gcp-hcp.test", got)
	}

	// Each cluster must get a distinct base domain so CPO-managed DNS
	// zones cannot collide.
	if other := cfg.variantBaseDomain("ingress"); other == got {
		t.Errorf("variantBaseDomain(ingress) = %q, want distinct from default %q", other, got)
	}
}

func TestGCPClusterSpecsBaseDomainIsPerCluster(t *testing.T) {
	t.Parallel()

	// The per-cluster --base-domain in CreateArgs must override the global
	// flag resolved from DefaultBaseDomain (pflag keeps the last value).
	cfg := &GCPPlatformConfig{
		prowJobID:   "job-123",
		ciDNSDomain: "example.gcp-hcp.test",
	}
	spec := cfg.ClusterSpecs(t.Context(), "release-image", "n1-image")[0]
	if got := spec.CreateArgs[0]; got != "--base-domain="+cfg.variantBaseDomain("default") {
		t.Errorf("CreateArgs[0] = %q, want --base-domain=%q", got, cfg.variantBaseDomain("default"))
	}
}

func TestGCPPreCreateChecksBaseDomainLength(t *testing.T) {
	cfg := &GCPPlatformConfig{prowJobID: "job-123"}
	//nolint:hypershiftlinter // unit test of PreCreate wiring; no Ginkgo TestContext here
	if err := cfg.PreCreate(context.TODO(), nil, ""); err != nil {
		t.Errorf("PreCreate(normal domain) = %v, want nil", err)
	}

	// A CI DNS domain long enough to push the generated base domain over
	// the DNS hostname limit must fail PreCreate, which cannot be
	// expressed by ClusterSpecs (it returns no error).
	cfg.ciDNSDomain = strings.Repeat("a", maxDNSHostnameLength)
	//nolint:hypershiftlinter // unit test of PreCreate wiring; no Ginkgo TestContext here
	err := cfg.PreCreate(context.TODO(), nil, "")
	if err == nil {
		t.Fatal("PreCreate(over-length domain) = nil, want error")
	}
	if !strings.Contains(err.Error(), "DNS hostname limit") {
		t.Errorf("PreCreate error %q should mention the DNS hostname limit", err)
	}
}

func TestGCPCheckBaseDomainLength(t *testing.T) {
	t.Parallel()

	if err := checkBaseDomainLength("default", "in.default-abc123.example.gcp-hcp.test"); err != nil {
		t.Errorf("checkBaseDomainLength(short domain) = %v, want nil", err)
	}

	// A domain at the limit passes; one past it fails with the variant and
	// domain identified, so CI config can be shortened deliberately
	// instead of cluster creation failing opaquely.
	if err := checkBaseDomainLength("default", strings.Repeat("a", 253)); err != nil {
		t.Errorf("checkBaseDomainLength(253-char domain) = %v, want nil", err)
	}
	err := checkBaseDomainLength("default", strings.Repeat("a", 254))
	if err == nil {
		t.Fatal("checkBaseDomainLength(254-char domain) = nil, want error")
	}
	for _, want := range []string{`variant "default"`, "254", "253"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("checkBaseDomainLength error %q missing %q", err.Error(), want)
		}
	}
}
