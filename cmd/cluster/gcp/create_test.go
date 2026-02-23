package gcp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/test/integration/framework"

	utilrand "k8s.io/apimachinery/pkg/util/rand"

	"github.com/spf13/pflag"
)

func TestCreateOptionsApplyPlatformSpecifics(t *testing.T) {
	g := NewGomegaWithT(t)

	opts := &CreateOptions{
		completedCreateOptions: &completedCreateOptions{
			ValidatedCreateOptions: &ValidatedCreateOptions{
				validatedCreateOptions: &validatedCreateOptions{
					RawCreateOptions: &RawCreateOptions{
						Project:                       "test-project-123",
						Region:                        "us-central1",
						Network:                       "test-network",
						PrivateServiceConnectSubnet:   "test-psc-subnet",
						WorkloadIdentityProjectNumber: "123456789012",
						WorkloadIdentityPoolID:        "test-pool-id",
						WorkloadIdentityProviderID:    "test-provider-id",
						NodePoolServiceAccount:        "nodepool@test-project-123.iam.gserviceaccount.com",
						ControlPlaneServiceAccount:    "controlplane@test-project-123.iam.gserviceaccount.com",
						CloudControllerServiceAccount: "cloudcontroller@test-project-123.iam.gserviceaccount.com",
					},
				},
			},
		},
	}

	hostedCluster := &hyperv1.HostedCluster{}

	err := opts.ApplyPlatformSpecifics(hostedCluster)
	g.Expect(err).To(BeNil())
	g.Expect(hostedCluster.Spec.Platform.Type).To(Equal(hyperv1.GCPPlatform))
	g.Expect(hostedCluster.Spec.Platform.GCP).ToNot(BeNil())
	g.Expect(hostedCluster.Spec.Platform.GCP.Project).To(Equal("test-project-123"))
	g.Expect(hostedCluster.Spec.Platform.GCP.Region).To(Equal("us-central1"))
	g.Expect(hostedCluster.Spec.Platform.GCP.NetworkConfig.Network.Name).To(Equal("test-network"))
	g.Expect(hostedCluster.Spec.Platform.GCP.NetworkConfig.PrivateServiceConnectSubnet.Name).To(Equal("test-psc-subnet"))
	g.Expect(hostedCluster.Spec.Platform.GCP.WorkloadIdentity.ProjectNumber).To(Equal("123456789012"))
	g.Expect(hostedCluster.Spec.Platform.GCP.WorkloadIdentity.PoolID).To(Equal("test-pool-id"))
	g.Expect(hostedCluster.Spec.Platform.GCP.WorkloadIdentity.ProviderID).To(Equal("test-provider-id"))
	g.Expect(hostedCluster.Spec.Platform.GCP.WorkloadIdentity.ServiceAccountsEmails.NodePool).To(Equal("nodepool@test-project-123.iam.gserviceaccount.com"))
	g.Expect(hostedCluster.Spec.Platform.GCP.WorkloadIdentity.ServiceAccountsEmails.ControlPlane).To(Equal("controlplane@test-project-123.iam.gserviceaccount.com"))
	g.Expect(hostedCluster.Spec.Platform.GCP.WorkloadIdentity.ServiceAccountsEmails.CloudController).To(Equal("cloudcontroller@test-project-123.iam.gserviceaccount.com"))
}

func TestValidateGCPOptions(t *testing.T) {
	g := NewGomegaWithT(t)

	validOpts := RawCreateOptions{
		Project:                       "test-project-123",
		Region:                        "us-central1",
		Network:                       "test-network",
		PrivateServiceConnectSubnet:   "test-psc-subnet",
		WorkloadIdentityProjectNumber: "123456789012",
		WorkloadIdentityPoolID:        "test-pool-id",
		WorkloadIdentityProviderID:    "test-provider-id",
		NodePoolServiceAccount:        "nodepool@test-project-123.iam.gserviceaccount.com",
		ControlPlaneServiceAccount:    "controlplane@test-project-123.iam.gserviceaccount.com",
		CloudControllerServiceAccount: "cloudcontroller@test-project-123.iam.gserviceaccount.com",
	}

	tests := map[string]struct {
		opts         RawCreateOptions
		expectErr    bool
		expectSubstr string
	}{
		"missing project": {
			opts:         RawCreateOptions{Region: validOpts.Region, Network: validOpts.Network, PrivateServiceConnectSubnet: validOpts.PrivateServiceConnectSubnet, WorkloadIdentityProjectNumber: validOpts.WorkloadIdentityProjectNumber, WorkloadIdentityPoolID: validOpts.WorkloadIdentityPoolID, WorkloadIdentityProviderID: validOpts.WorkloadIdentityProviderID, NodePoolServiceAccount: validOpts.NodePoolServiceAccount, ControlPlaneServiceAccount: validOpts.ControlPlaneServiceAccount, CloudControllerServiceAccount: validOpts.CloudControllerServiceAccount},
			expectErr:    true,
			expectSubstr: "required flag(s) \"project\" not set",
		},
		"missing region": {
			opts:         RawCreateOptions{Project: validOpts.Project, Network: validOpts.Network, PrivateServiceConnectSubnet: validOpts.PrivateServiceConnectSubnet, WorkloadIdentityProjectNumber: validOpts.WorkloadIdentityProjectNumber, WorkloadIdentityPoolID: validOpts.WorkloadIdentityPoolID, WorkloadIdentityProviderID: validOpts.WorkloadIdentityProviderID, NodePoolServiceAccount: validOpts.NodePoolServiceAccount, ControlPlaneServiceAccount: validOpts.ControlPlaneServiceAccount, CloudControllerServiceAccount: validOpts.CloudControllerServiceAccount},
			expectErr:    true,
			expectSubstr: "required flag(s) \"region\" not set",
		},
		"missing network": {
			opts:         RawCreateOptions{Project: validOpts.Project, Region: validOpts.Region, PrivateServiceConnectSubnet: validOpts.PrivateServiceConnectSubnet, WorkloadIdentityProjectNumber: validOpts.WorkloadIdentityProjectNumber, WorkloadIdentityPoolID: validOpts.WorkloadIdentityPoolID, WorkloadIdentityProviderID: validOpts.WorkloadIdentityProviderID, NodePoolServiceAccount: validOpts.NodePoolServiceAccount, ControlPlaneServiceAccount: validOpts.ControlPlaneServiceAccount, CloudControllerServiceAccount: validOpts.CloudControllerServiceAccount},
			expectErr:    true,
			expectSubstr: "required flag(s) \"network\" not set",
		},
		"missing cloud-controller-service-account": {
			opts:         RawCreateOptions{Project: validOpts.Project, Region: validOpts.Region, Network: validOpts.Network, PrivateServiceConnectSubnet: validOpts.PrivateServiceConnectSubnet, WorkloadIdentityProjectNumber: validOpts.WorkloadIdentityProjectNumber, WorkloadIdentityPoolID: validOpts.WorkloadIdentityPoolID, WorkloadIdentityProviderID: validOpts.WorkloadIdentityProviderID, NodePoolServiceAccount: validOpts.NodePoolServiceAccount, ControlPlaneServiceAccount: validOpts.ControlPlaneServiceAccount},
			expectErr:    true,
			expectSubstr: "required flag(s) \"cloud-controller-service-account\" not set",
		},
		"all required fields provided": {
			opts:      validOpts,
			expectErr: false,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := tc.opts.Validate(context.Background(), &core.CreateOptions{})
			if tc.expectErr {
				g.Expect(err).To(HaveOccurred())
				if tc.expectSubstr != "" {
					g.Expect(err.Error()).To(ContainSubstring(tc.expectSubstr))
				}
			} else {
				g.Expect(err).To(BeNil())
			}
		})
	}
}

func TestCreateCluster(t *testing.T) {
	utilrand.Seed(1234567890)
	certs.UnsafeSeed(1234567890)
	ctx := framework.InterruptableContext(t.Context())
	tempDir := t.TempDir()
	t.Setenv("FAKE_CLIENT", "true")

	pullSecretFile := filepath.Join(tempDir, "pull-secret.json")
	if err := os.WriteFile(pullSecretFile, []byte(`fake`), 0600); err != nil {
		t.Fatalf("failed to write pullSecret: %v", err)
	}

	for _, testCase := range []struct {
		name string
		args []string
	}{
		{
			name: "minimal flags necessary to render",
			args: []string{
				"--project=test-project-123",
				"--region=us-central1",
				"--network=test-network",
				"--private-service-connect-subnet=test-psc-subnet",
				"--workload-identity-project-number=123456789012",
				"--workload-identity-pool-id=test-pool",
				"--workload-identity-provider-id=test-provider",
				"--node-pool-service-account=nodepool@test-project-123.iam.gserviceaccount.com",
				"--control-plane-service-account=controlplane@test-project-123.iam.gserviceaccount.com",
				"--cloud-controller-service-account=cloudcontroller@test-project-123.iam.gserviceaccount.com",
				"--node-pool-replicas=-1",
				"--name=example",
				"--pull-secret=" + pullSecretFile,
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			flags := pflag.NewFlagSet(testCase.name, pflag.ContinueOnError)
			coreOpts := core.DefaultOptions()
			core.BindDeveloperOptions(coreOpts, flags)
			gcpOpts := DefaultOptions()
			BindOptions(gcpOpts, flags)
			if err := flags.Parse(testCase.args); err != nil {
				t.Fatalf("failed to parse flags: %v", err)
			}

			tempDir := t.TempDir()
			manifestsFile := filepath.Join(tempDir, "manifests.yaml")
			coreOpts.Render = true
			coreOpts.RenderInto = manifestsFile

			if err := core.CreateCluster(ctx, coreOpts, gcpOpts); err != nil {
				t.Fatalf("failed to create cluster: %v", err)
			}

			manifests, err := os.ReadFile(manifestsFile)
			if err != nil {
				t.Fatalf("failed to read manifests file: %v", err)
			}
			testutil.CompareWithFixture(t, manifests)
		})
	}
}
