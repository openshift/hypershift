package gcp

import (
	"context"
	"errors"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	gcpinfra "github.com/openshift/hypershift/cmd/infra/gcp"
	"github.com/openshift/hypershift/cmd/log"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-logr/logr"
)

const validInfraAnnotation = `{"version":1,"router":"recorded-router","nat":"recorded-nat","firewallRule":"recorded-firewall"}`

func validGCPServiceAccountEmails() hyperv1.GCPServiceAccountsEmails {
	return hyperv1.GCPServiceAccountsEmails{
		NodePool: "nodepool@test-project.iam.gserviceaccount.com", ControlPlane: "controlplane@test-project.iam.gserviceaccount.com",
		CloudController: "cloudcontroller@test-project.iam.gserviceaccount.com", Storage: "storage@test-project.iam.gserviceaccount.com",
		ImageRegistry: "image-registry@test-project.iam.gserviceaccount.com", Network: "network@test-project.iam.gserviceaccount.com",
	}
}

func validGCPPlatformSpec() *hyperv1.GCPPlatformSpec {
	return &hyperv1.GCPPlatformSpec{
		Project: "test-project", Region: "us-central1",
		NetworkConfig: hyperv1.GCPNetworkConfig{
			Network:                     hyperv1.GCPResourceReference{Name: "recorded-network"},
			PrivateServiceConnectSubnet: hyperv1.GCPResourceReference{Name: "recorded-subnet"},
		},
		WorkloadIdentity: hyperv1.GCPWorkloadIdentityConfig{
			ProjectNumber: "987654321",
			PoolID:        "recorded-pool", ProviderID: "recorded-provider", ServiceAccountsEmails: validGCPServiceAccountEmails(),
		},
	}
}

func validDestroyCluster() *hyperv1.HostedCluster {
	return &hyperv1.HostedCluster{
		ObjectMeta: metav1.ObjectMeta{Annotations: map[string]string{infraResourcesAnnotation: validInfraAnnotation}},
		Spec:       hyperv1.HostedClusterSpec{InfraID: "test-infra", Platform: hyperv1.PlatformSpec{Type: hyperv1.GCPPlatform, GCP: validGCPPlatformSpec()}},
	}
}

func validDestroyOptions() *core.DestroyOptions {
	return &core.DestroyOptions{
		InfraID: "test-infra", Log: log.Log,
		GCPPlatform: core.GCPPlatformDestroyOptions{
			ProjectID: "test-project", Region: "us-central1",
			NetworkName: "recorded-network", SubnetName: "recorded-subnet", RouterName: "recorded-router",
			NATName: "recorded-nat", FirewallRuleName: "recorded-firewall",
			WorkloadIdentityProjectNumber: "987654321",
			WorkloadIdentityPoolID:        "recorded-pool", WorkloadIdentityProviderID: "recorded-provider",
			ServiceAccountEmails: validGCPServiceAccountEmails(),
		},
	}
}

// Expected options are independent of the production mapping so a mapping bug
// cannot pass by producing the same incorrect values on both sides of an assertion.
func expectedCleanupOptions() (gcpinfra.DestroyIAMOptions, gcpinfra.DestroyInfraOptions) {
	return gcpinfra.DestroyIAMOptions{
			ProjectID: "test-project", PoolID: "recorded-pool", ProviderID: "recorded-provider",
			WorkloadIdentityProjectNumber: "987654321",
			ServiceAccountEmails: map[string]string{
				"nodepool-mgmt": "nodepool@test-project.iam.gserviceaccount.com", "ctrlplane-op": "controlplane@test-project.iam.gserviceaccount.com",
				"cloud-controller": "cloudcontroller@test-project.iam.gserviceaccount.com", "gcp-pd-csi": "storage@test-project.iam.gserviceaccount.com",
				"image-registry": "image-registry@test-project.iam.gserviceaccount.com", "cloud-network": "network@test-project.iam.gserviceaccount.com",
			},
		}, gcpinfra.DestroyInfraOptions{
			ProjectID: "test-project", Region: "us-central1",
			Resources: gcpinfra.NetworkResourceNames{Network: "recorded-network", Subnet: "recorded-subnet", Router: "recorded-router", NAT: "recorded-nat", FirewallRule: "recorded-firewall"},
		}
}

// recordCleanup checks the complete options passed to each cloud cleanup stage.
func recordCleanup(t *testing.T, calls *[]string, iamErr, infraErr error) {
	t.Helper()
	g := NewGomegaWithT(t)
	originalIAM, originalInfra := runDestroyIAM, runDestroyInfra
	t.Cleanup(func() { runDestroyIAM, runDestroyInfra = originalIAM, originalInfra })
	expectedIAM, expectedInfra := expectedCleanupOptions()
	runDestroyIAM = func(_ context.Context, opts gcpinfra.DestroyIAMOptions, _ logr.Logger) error {
		*calls = append(*calls, "IAM")
		g.Expect(opts).To(Equal(expectedIAM))
		return iamErr
	}
	runDestroyInfra = func(_ context.Context, opts gcpinfra.DestroyInfraOptions, _ logr.Logger) error {
		*calls = append(*calls, "Infra")
		g.Expect(opts).To(Equal(expectedInfra))
		return infraErr
	}
}

func TestNewDestroyCommand(t *testing.T) {
	for _, test := range []struct {
		name       string
		flags      []string
		iam, infra bool
	}{
		{name: "When no preserve flags are set, it should clean up both resource types"},
		{name: "When both preserve flags are set, it should preserve both resource types", flags: []string{"--preserve-iam", "--preserve-infra"}, iam: true, infra: true},
		{name: "When only IAM is preserved, it should keep infrastructure cleanup enabled", flags: []string{"--preserve-iam"}, iam: true},
		{name: "When only infrastructure is preserved, it should keep IAM cleanup enabled", flags: []string{"--preserve-infra"}, infra: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			opts := &core.DestroyOptions{Log: log.Log}
			cmd := NewDestroyCommand(opts)
			g.Expect(cmd.Use).To(Equal("gcp"))
			g.Expect(cmd.ParseFlags(test.flags)).To(Succeed())
			g.Expect(opts.GCPPlatform.PreserveIAM).To(Equal(test.iam))
			g.Expect(opts.GCPPlatform.PreserveInfra).To(Equal(test.infra))
		})
	}
}

func TestExtractParameters(t *testing.T) {
	for _, test := range []struct{ name, initialInfraID string }{
		{name: "When references are recorded, it should extract every reference from the HostedCluster"},
		{name: "When caller supplied references differ, it should use the HostedCluster references", initialInfraID: "caller-infra"},
	} {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			opts := &core.DestroyOptions{InfraID: test.initialInfraID, GCPPlatform: core.GCPPlatformDestroyOptions{
				ProjectID: "caller-project", Region: "caller-region", NetworkName: "caller-network", SubnetName: "caller-subnet",
				RouterName: "caller-router", NATName: "caller-nat", FirewallRuleName: "caller-firewall",
				WorkloadIdentityProjectNumber: "111111111",
				WorkloadIdentityPoolID:        "caller-pool", WorkloadIdentityProviderID: "caller-provider",
			}}
			g.Expect(extractParameters(validDestroyCluster(), opts)).To(Succeed())
			g.Expect(opts.InfraID).To(Equal("test-infra"))
			g.Expect(opts.GCPPlatform).To(Equal(validDestroyOptions().GCPPlatform))
		})
	}
}

func TestCleanupOptions(t *testing.T) {
	g := NewGomegaWithT(t)
	iam, infra := cleanupOptions(validDestroyOptions())
	expectedIAM, expectedInfra := expectedCleanupOptions()
	g.Expect(iam).To(Equal(expectedIAM))
	g.Expect(infra).To(Equal(expectedInfra))
}

func TestDestroyPlatformSpecifics(t *testing.T) {
	iamErr, infraErr := errors.New("IAM cleanup failed"), errors.New("infrastructure cleanup failed")
	for _, test := range []struct {
		name                       string
		preserveIAM, preserveInfra bool
		iamErr, infraErr           error
		calls                      []string
	}{
		{name: "When both resources are preserved, it should skip cloud deletion", preserveIAM: true, preserveInfra: true},
		{name: "When IAM is preserved, it should delete only infrastructure", preserveIAM: true, calls: []string{"Infra"}},
		{name: "When infrastructure is preserved, it should delete only IAM", preserveInfra: true, calls: []string{"IAM"}},
		{name: "When neither resource is preserved, it should delete IAM before infrastructure", calls: []string{"IAM", "Infra"}},
		{name: "When IAM deletion fails, it should still delete infrastructure and return the error", iamErr: iamErr, calls: []string{"IAM", "Infra"}},
		{name: "When infrastructure deletion fails, it should return the error", infraErr: infraErr, calls: []string{"IAM", "Infra"}},
		{name: "When both deletions fail, it should retain both errors", iamErr: iamErr, infraErr: infraErr, calls: []string{"IAM", "Infra"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			var calls []string
			recordCleanup(t, &calls, test.iamErr, test.infraErr)
			opts := validDestroyOptions()
			opts.GCPPlatform.PreserveIAM, opts.GCPPlatform.PreserveInfra = test.preserveIAM, test.preserveInfra
			err := destroyPlatformSpecifics(context.Background(), opts, nil)
			g.Expect(calls).To(Equal(test.calls))
			g.Expect(errors.Is(err, iamErr)).To(Equal(test.iamErr != nil))
			g.Expect(errors.Is(err, infraErr)).To(Equal(test.infraErr != nil))
			if test.iamErr == nil && test.infraErr == nil {
				g.Expect(err).ToNot(HaveOccurred())
			}
		})
	}
}

func TestDestroyCluster(t *testing.T) {
	for _, test := range []struct {
		name                                       string
		mutate                                     func(*hyperv1.HostedCluster)
		missingCluster, preserveIAM, preserveInfra bool
		getError                                   error
		errorText                                  string
		calls                                      []string
	}{
		{name: "When the HostedCluster is missing, it should return manual cleanup guidance", missingCluster: true, errorText: "Cannot destroy infrastructure without cluster as source of truth"},
		{name: "When the cluster lookup fails, it should stop before deletion", getError: errors.New("lookup failed"), errorText: "lookup failed"},
		{name: "When a legacy cluster has no annotation, it should stop before deletion", mutate: func(hc *hyperv1.HostedCluster) { hc.Annotations = nil }, errorText: "missing annotation"},
		{name: "When an IAM account reference is missing, it should stop before deletion", mutate: func(hc *hyperv1.HostedCluster) {
			hc.Spec.Platform.GCP.WorkloadIdentity.ServiceAccountsEmails.Storage = ""
		}, errorText: "service account email for gcp-pd-csi is required"},
		{name: "When the WIF project number is missing, it should stop before deletion", mutate: func(hc *hyperv1.HostedCluster) {
			hc.Spec.Platform.GCP.WorkloadIdentity.ProjectNumber = ""
		}, errorText: "workload identity project number is required; use --preserve-iam"},
		{name: "When the WIF project number is invalid, it should stop before deletion", mutate: func(hc *hyperv1.HostedCluster) {
			hc.Spec.Platform.GCP.WorkloadIdentity.ProjectNumber = "other-project"
		}, errorText: "workload identity project number must contain only digits; use --preserve-iam"},
		{name: "When references are valid, it should delete the cluster and invoke cleanup", calls: []string{"delete", "IAM", "Infra"}},
		{name: "When annotation JSON is malformed, it should stop before deletion", mutate: func(hc *hyperv1.HostedCluster) { hc.Annotations[infraResourcesAnnotation] = "{" }, errorText: "invalid HostedCluster annotation"},
		{name: "When the annotation version is unsupported, it should stop before deletion", mutate: func(hc *hyperv1.HostedCluster) {
			hc.Annotations[infraResourcesAnnotation] = `{"version":2,"router":"router","nat":"nat","firewallRule":"firewall"}`
		}, errorText: "unsupported GCP infrastructure resources version"},
		{name: "When metadata is malformed and infrastructure is preserved, it should delete the cluster and IAM", mutate: func(hc *hyperv1.HostedCluster) { hc.Annotations[infraResourcesAnnotation] = "{" }, preserveInfra: true, calls: []string{"delete", "IAM"}},
		{name: "When legacy metadata is missing and infrastructure is preserved, it should delete the cluster and IAM", mutate: func(hc *hyperv1.HostedCluster) { hc.Annotations = nil }, preserveInfra: true, calls: []string{"delete", "IAM"}},
		{name: "When IAM is preserved, it should delete the cluster and only infrastructure", preserveIAM: true, calls: []string{"delete", "Infra"}},
		{name: "When the WIF project number is missing and IAM is preserved, it should delete only the cluster and infrastructure", mutate: func(hc *hyperv1.HostedCluster) {
			hc.Spec.Platform.GCP.WorkloadIdentity.ProjectNumber = ""
		}, preserveIAM: true, calls: []string{"delete", "Infra"}},
		{name: "When the WIF project number is invalid and IAM is preserved, it should delete only the cluster and infrastructure", mutate: func(hc *hyperv1.HostedCluster) {
			hc.Spec.Platform.GCP.WorkloadIdentity.ProjectNumber = "other-project"
		}, preserveIAM: true, calls: []string{"delete", "Infra"}},
		{name: "When all resources are preserved and InfraID is empty, it should invoke cluster deletion", mutate: func(hc *hyperv1.HostedCluster) {
			hc.Spec.InfraID = ""
			hc.Spec.Platform.GCP = &hyperv1.GCPPlatformSpec{}
			hc.Annotations = nil
		}, preserveIAM: true, preserveInfra: true, calls: []string{"delete"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			hc := validDestroyCluster()
			if test.mutate != nil {
				test.mutate(hc)
			}
			if test.missingCluster {
				hc = nil
			}
			originalGet, originalDestroy := getCluster, destroyCluster
			t.Cleanup(func() {
				getCluster, destroyCluster = originalGet, originalDestroy
			})
			var calls []string
			recordCleanup(t, &calls, nil, nil)
			getCluster = func(context.Context, crclient.Client, *core.DestroyOptions) (*hyperv1.HostedCluster, error) {
				return hc, test.getError
			}
			destroyCluster = func(ctx context.Context, client crclient.Client, cluster *hyperv1.HostedCluster, opts *core.DestroyOptions, cleanup core.DestroyPlatformSpecifics) error {
				calls = append(calls, "delete")
				g.Expect(cluster).To(BeIdenticalTo(hc))
				g.Expect(opts.InfraID).To(Equal(hc.Spec.InfraID))
				g.Expect(opts.GCPPlatform.PreserveIAM).To(Equal(test.preserveIAM))
				g.Expect(opts.GCPPlatform.PreserveInfra).To(Equal(test.preserveInfra))
				g.Expect(cleanup).ToNot(BeNil())
				return cleanup(ctx, opts, client)
			}
			err := DestroyCluster(context.Background(), &core.DestroyOptions{Name: "test-cluster", Namespace: "clusters", Log: log.Log, GCPPlatform: core.GCPPlatformDestroyOptions{PreserveIAM: test.preserveIAM, PreserveInfra: test.preserveInfra}}, nil)
			expectError(t, err, test.errorText)
			g.Expect(calls).To(Equal(test.calls))
		})
	}
}

func TestValidateInputs(t *testing.T) {
	for _, test := range []struct {
		name      string
		mutate    func(*core.GCPPlatformDestroyOptions)
		errorText string
	}{
		{name: "When exact references are present, it should pass without InfraID"},
		{name: "When region is missing, it should reject infrastructure cleanup", mutate: func(p *core.GCPPlatformDestroyOptions) { p.Region = "" }, errorText: "region is required"},
		{name: "When the network is missing, it should reject infrastructure cleanup", mutate: func(p *core.GCPPlatformDestroyOptions) { p.NetworkName = "" }, errorText: "network resource name is required"},
		{name: "When the subnet is missing, it should reject infrastructure cleanup", mutate: func(p *core.GCPPlatformDestroyOptions) { p.SubnetName = "" }, errorText: "subnet resource name is required"},
		{name: "When the router is missing, it should reject infrastructure cleanup", mutate: func(p *core.GCPPlatformDestroyOptions) { p.RouterName = "" }, errorText: "router resource name is required"},
		{name: "When NAT is missing, it should reject infrastructure cleanup", mutate: func(p *core.GCPPlatformDestroyOptions) { p.NATName = "" }, errorText: "NAT resource name is required"},
		{name: "When the firewall is missing, it should reject infrastructure cleanup", mutate: func(p *core.GCPPlatformDestroyOptions) { p.FirewallRuleName = "" }, errorText: "firewall rule resource name is required"},
		{name: "When the WIF project number is missing, it should reject IAM cleanup", mutate: func(p *core.GCPPlatformDestroyOptions) { p.WorkloadIdentityProjectNumber = "" }, errorText: "workload identity project number is required"},
		{name: "When the WIF project number is invalid, it should reject IAM cleanup", mutate: func(p *core.GCPPlatformDestroyOptions) { p.WorkloadIdentityProjectNumber = "other-project" }, errorText: "workload identity project number must contain only digits"},
		{name: "When the pool is missing, it should reject IAM cleanup", mutate: func(p *core.GCPPlatformDestroyOptions) { p.WorkloadIdentityPoolID = "" }, errorText: "workload identity pool ID is required"},
		{name: "When the provider is missing, it should reject IAM cleanup", mutate: func(p *core.GCPPlatformDestroyOptions) { p.WorkloadIdentityProviderID = "" }, errorText: "workload identity provider ID is required"},
		{name: "When project is missing, it should reject requested cleanup", mutate: func(p *core.GCPPlatformDestroyOptions) { p.ProjectID = "" }, errorText: "project ID is required"},
		{name: "When infrastructure is preserved, it should allow missing networking references", mutate: func(p *core.GCPPlatformDestroyOptions) {
			p.NetworkName = ""
			p.RouterName = ""
			p.Region = ""
			p.PreserveInfra = true
		}},
		{name: "When IAM is preserved, it should allow missing IAM references", mutate: func(p *core.GCPPlatformDestroyOptions) {
			p.WorkloadIdentityProjectNumber = ""
			p.WorkloadIdentityPoolID = ""
			p.WorkloadIdentityProviderID = ""
			p.ServiceAccountEmails = hyperv1.GCPServiceAccountsEmails{}
			p.PreserveIAM = true
		}},
		{name: "When both types are preserved, it should allow empty cleanup inputs", mutate: func(p *core.GCPPlatformDestroyOptions) {
			*p = core.GCPPlatformDestroyOptions{PreserveIAM: true, PreserveInfra: true}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			opts := validDestroyOptions()
			opts.InfraID = ""
			if test.mutate != nil {
				test.mutate(&opts.GCPPlatform)
			}
			err := validateInputs(opts)
			expectError(t, err, test.errorText)
		})
	}
	for _, account := range []struct {
		name  string
		clear func(*hyperv1.GCPServiceAccountsEmails)
	}{
		{"nodepool-mgmt", func(a *hyperv1.GCPServiceAccountsEmails) { a.NodePool = "" }},
		{"ctrlplane-op", func(a *hyperv1.GCPServiceAccountsEmails) { a.ControlPlane = "" }},
		{"cloud-controller", func(a *hyperv1.GCPServiceAccountsEmails) { a.CloudController = "" }},
		{"gcp-pd-csi", func(a *hyperv1.GCPServiceAccountsEmails) { a.Storage = "" }},
		{"image-registry", func(a *hyperv1.GCPServiceAccountsEmails) { a.ImageRegistry = "" }},
		{"cloud-network", func(a *hyperv1.GCPServiceAccountsEmails) { a.Network = "" }},
	} {
		t.Run("When the "+account.name+" account is missing, it should reject IAM cleanup", func(t *testing.T) {
			g := NewGomegaWithT(t)
			opts := validDestroyOptions()
			account.clear(&opts.GCPPlatform.ServiceAccountEmails)
			g.Expect(validateInputs(opts)).To(MatchError(ContainSubstring("service account email for " + account.name + " is required")))
		})
	}
}
