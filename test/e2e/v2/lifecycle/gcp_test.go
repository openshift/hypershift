//go:build e2ev2

package lifecycle

import (
	"slices"
	"testing"
)

func TestGCPClusterSpecs(t *testing.T) {
	t.Parallel()

	specs := (&GCPPlatformConfig{}).ClusterSpecs("release-image", "n1-image")
	if len(specs) != 1 {
		t.Fatalf("got %d GCP variants, want 1", len(specs))
	}

	spec := specs[0]
	if spec.Variant != "default" {
		t.Fatalf("GCP variant %q, want %q", spec.Variant, "default")
	}

	// The default variant must reproduce today's single-cluster e2e-v2-gke
	// behavior exactly: Console and Ingress disabled, TechPreviewNoUpgrade.
	wantArgs := []string{
		"--disable-cluster-capabilities=Console",
		"--disable-cluster-capabilities=Ingress",
		"--feature-set=TechPreviewNoUpgrade",
	}
	for _, arg := range wantArgs {
		if !slices.Contains(spec.ExtraArgs, arg) {
			t.Errorf("default ExtraArgs %v missing %q", spec.ExtraArgs, arg)
		}
	}
}

func TestGCPClusterSpecsHonorsExtraArgs(t *testing.T) {
	t.Setenv("EXTRA_ARGS", "--foo=bar --baz")

	specs := (&GCPPlatformConfig{}).ClusterSpecs("release-image", "n1-image")
	if len(specs) != 1 {
		t.Fatalf("got %d GCP variants, want 1", len(specs))
	}
	for _, arg := range []string{"--foo=bar", "--baz"} {
		if !slices.Contains(specs[0].ExtraArgs, arg) {
			t.Errorf("default ExtraArgs %v missing EXTRA_ARGS entry %q", specs[0].ExtraArgs, arg)
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
	if err := plan.Validate(cfg.ClusterSpecs("release-image", "n1-image")); err != nil {
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

func TestGCPCreateArgs(t *testing.T) {
	t.Parallel()

	cfg := &GCPPlatformConfig{
		project:     "my-project",
		region:      "us-central1",
		zone:        "us-central1-a",
		ciDNSDomain: "example.gcp-hcp.test",
		bootImage:   "projects/rhcos-cloud/global/images/boot",
	}

	wantArgs := []string{
		"--project=my-project",
		"--region=us-central1",
		"--endpoint-access=PublicAndPrivate",
		"--annotations=hypershift.openshift.io/pod-security-admission-label-override=baseline",
		"--zone=us-central1-a",
		"--external-dns-domain=example.gcp-hcp.test",
		"--boot-image=projects/rhcos-cloud/global/images/boot",
	}
	got := cfg.CreateArgs()
	for _, arg := range wantArgs {
		if !slices.Contains(got, arg) {
			t.Errorf("CreateArgs() %v missing %q", got, arg)
		}
	}
}

func TestGCPVariantCreateArgs(t *testing.T) {
	t.Parallel()

	infra := gcpVariantInfra{
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

	// Each flag encodes a distinct cluster-creation contract that a
	// regression could silently drop.
	wantArgs := []string{
		"--network=vpc-net",
		"--private-service-connect-subnet=psc-subnet",
		"--workload-identity-project-number=123456",
		"--workload-identity-pool-id=pool",
		"--workload-identity-provider-id=provider",
		"--control-plane-service-account=cp@sa",
		"--node-pool-service-account=np@sa",
		"--cloud-controller-service-account=cc@sa",
		"--storage-service-account=storage@sa",
		"--image-registry-service-account=ir@sa",
		"--network-service-account=net@sa",
		"--service-account-signing-key-path=/shared/sa-signer.key",
		"--oidc-issuer-url=https://hypershift-ci123-oidc",
	}
	got := variantCreateArgs(infra)
	if len(got) != len(wantArgs) {
		t.Fatalf("variantCreateArgs() returned %d args, want %d: %v", len(got), len(wantArgs), got)
	}
	for _, arg := range wantArgs {
		if !slices.Contains(got, arg) {
			t.Errorf("variantCreateArgs() %v missing %q", got, arg)
		}
	}
}

func TestGCPCreateArgsForVariant(t *testing.T) {
	t.Parallel()

	cfg := &GCPPlatformConfig{variantInfra: map[string]gcpVariantInfra{}}

	// Before PreCreate resolves infra, no per-variant args are available.
	if got := cfg.CreateArgsForVariant("default"); got != nil {
		t.Errorf("CreateArgsForVariant before PreCreate = %v, want nil", got)
	}

	cfg.variantInfra["default"] = gcpVariantInfra{oidcIssuerURL: "https://issuer"}
	if got := cfg.CreateArgsForVariant("default"); !slices.Contains(got, "--oidc-issuer-url=https://issuer") {
		t.Errorf("CreateArgsForVariant(default) = %v, want it to contain the resolved issuer flag", got)
	}
}

func TestGCPDestroyArgs(t *testing.T) {
	t.Parallel()

	if got := (&GCPPlatformConfig{}).DestroyArgs(); got != nil {
		t.Errorf("DestroyArgs() = %v, want nil", got)
	}
}
