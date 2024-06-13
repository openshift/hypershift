package azure

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/openshift/hypershift/cmd/cluster/core"
	azureinfra "github.com/openshift/hypershift/cmd/infra/azure"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/test/integration/framework"
	"github.com/spf13/pflag"
	"sigs.k8s.io/yaml"
)

func TestCreateCluster(t *testing.T) {
	ctx := framework.InterruptableContext(context.Background())
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
		MachineIdentityID: "fakeMachineIdentityID",
		SecurityGroupID:   "fakeSecurityGroupID",
	})
	if err != nil {
		t.Fatalf("failed to marshal infra: %v", err)
	}
	infraFile := filepath.Join(tempDir, "infra.json")
	if err := os.WriteFile(infraFile, rawInfra, 0600); err != nil {
		t.Fatalf("failed to write infra: %v", err)
	}

	for _, testCase := range []struct {
		name string
		args []string
	}{
		{
			name: "minimal flags necessary to render",
			args: []string{
				"--azure-creds=" + credentialsFile,
				"--infra-json=" + infraFile,
				"--rhcos-image=whatever",
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			flags := pflag.NewFlagSet(testCase.name, pflag.ContinueOnError)
			coreOpts := core.DefaultOptions()
			core.BindDeveloperOptions(coreOpts, flags)
			azureOpts := DefaultOptions()
			BindDeveloperOptions(azureOpts, flags)
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
