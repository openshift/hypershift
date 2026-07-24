package azure

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/gomega"

	hypershiftazure "github.com/openshift/hypershift/cmd/cluster/azure"
	"github.com/openshift/hypershift/cmd/cluster/core"
	azureinfra "github.com/openshift/hypershift/cmd/infra/azure"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/test/integration/framework"

	utilrand "k8s.io/apimachinery/pkg/util/rand"

	"sigs.k8s.io/yaml"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func TestNewCreateCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		test func(t *testing.T)
	}{
		{
			name: "When Azure create command is created, it should have 'azure' as use",
			test: func(t *testing.T) {
				g := NewWithT(t)
				opts := core.DefaultOptions()
				cmd := NewCreateCommand(opts)
				g.Expect(cmd.Use).To(Equal("azure"))
			},
		},
		{
			name: "When Azure create command is created, it should mark azure-creds as required",
			test: func(t *testing.T) {
				g := NewWithT(t)
				opts := core.DefaultOptions()
				cmd := NewCreateCommand(opts)

				azureCredsFlag := cmd.Flag("azure-creds")
				g.Expect(azureCredsFlag).ToNot(BeNil())
				g.Expect(azureCredsFlag.Annotations).To(HaveKey(cobra.BashCompOneRequiredFlag))
				g.Expect(azureCredsFlag.Annotations[cobra.BashCompOneRequiredFlag]).To(ContainElement("true"))
			},
		},
		{
			name: "When Azure create command is created, it should mark pull-secret as required",
			test: func(t *testing.T) {
				g := NewWithT(t)
				opts := core.DefaultOptions()

				// pull-secret is registered as a persistent flag on the parent
				// command (see product-cli/cmd/cluster/cluster.go). Simulate
				// that hierarchy here so MarkPersistentFlagRequired resolves
				// the flag correctly.
				parent := &cobra.Command{Use: "cluster"}
				core.BindOptions(opts, parent.PersistentFlags())
				_ = parent.MarkPersistentFlagRequired("pull-secret")

				cmd := NewCreateCommand(opts)
				parent.AddCommand(cmd)

				pullSecretFlag := parent.PersistentFlags().Lookup("pull-secret")
				g.Expect(pullSecretFlag).ToNot(BeNil())
				g.Expect(pullSecretFlag.Annotations).To(HaveKey(cobra.BashCompOneRequiredFlag))
				g.Expect(pullSecretFlag.Annotations[cobra.BashCompOneRequiredFlag]).To(ContainElement("true"))
			},
		},
		{
			name: "When Azure create command is created, it should set release stream to default",
			test: func(t *testing.T) {
				g := NewWithT(t)
				opts := core.DefaultOptions()
				_ = NewCreateCommand(opts)
				g.Expect(opts.ReleaseStream).To(Equal(config.DefaultReleaseStream))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tt.test(t)
		})
	}
}

func TestCreateCluster(t *testing.T) {
	utilrand.Seed(1234567890)
	certs.UnsafeSeed(1234567890)
	ctx := framework.InterruptableContext(t.Context())
	t.Setenv("FAKE_CLIENT", "true")

	tempDir := t.TempDir()

	rawCreds, err := yaml.Marshal(&util.AzureCreds{
		SubscriptionID: "fakeSubscriptionID",
		ClientID:       "fakeClientID",
		ClientSecret:   "fakeClientSecret",
		TenantID:       "fakeTenantID",
	})
	if err != nil {
		t.Fatalf("failed to marshal creds: %v", err)
	}
	credentialsFile := filepath.Join(tempDir, "credentials.yaml")
	if err := os.WriteFile(credentialsFile, rawCreds, 0600); err != nil {
		t.Fatalf("failed to write creds: %v", err)
	}

	rawInfra, err := json.Marshal(&azureinfra.CreateInfraOutput{
		BaseDomain:        "fakeBaseDomain",
		PublicZoneID:      "fakePublicZoneID",
		PrivateZoneID:     "fakePrivateZoneID",
		Location:          "fakeLocation",
		ResourceGroupName: "fakeResourceGroupName",
		VNetID:            "fakeVNetID",
		SubnetID:          "fakeSubnetID",
		BootImageID:       "fakeBootImageID",
		InfraID:           "fakeInfraID",
		SecurityGroupID:   "fakeSecurityGroupID",
	})
	if err != nil {
		t.Fatalf("failed to marshal infra: %v", err)
	}
	infraFile := filepath.Join(tempDir, "infra.json")
	if err := os.WriteFile(infraFile, rawInfra, 0600); err != nil {
		t.Fatalf("failed to write infra: %v", err)
	}

	pullSecretFile := filepath.Join(tempDir, "pull-secret.json")
	if err := os.WriteFile(pullSecretFile, []byte(`fake`), 0600); err != nil {
		t.Fatalf("failed to write pullSecret: %v", err)
	}

	for _, testCase := range []struct {
		name string
		args []string
	}{
		{
			name: "When minimal flags are provided, it should render correctly",
			args: []string{
				"--azure-creds=" + credentialsFile,
				"--infra-json=" + infraFile,
				"--render-sensitive",
				"--name=example",
				"--pull-secret=" + pullSecretFile,
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			flags := pflag.NewFlagSet(testCase.name, pflag.ContinueOnError)
			coreOpts := core.DefaultOptions()
			core.BindOptions(coreOpts, flags)
			hypershiftazure.BindProductCoreFlags(coreOpts, flags)
			azureOpts := hypershiftazure.DefaultOptions()
			hypershiftazure.BindProductFlags(azureOpts, flags)
			if err := flags.Parse(testCase.args); err != nil {
				t.Fatalf("failed to parse flags: %v", err)
			}

			tempDir := t.TempDir()
			manifestsFile := filepath.Join(tempDir, "manifests.yaml")
			coreOpts.Render = true
			coreOpts.RenderInto = manifestsFile

			if err := core.CreateCluster(ctx, coreOpts, azureOpts); err != nil {
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
