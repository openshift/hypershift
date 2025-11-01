package azure

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	azureinfra "github.com/openshift/hypershift/cmd/infra/azure"
	azurenodepool "github.com/openshift/hypershift/cmd/nodepool/azure"
	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/test/integration/framework"

	utilrand "k8s.io/apimachinery/pkg/util/rand"

	"sigs.k8s.io/yaml"

	"github.com/spf13/pflag"
)

func TestCreateCluster(t *testing.T) {
	utilrand.Seed(1234567890)
	certs.UnsafeSeed(1234567890)
	ctx := framework.InterruptableContext(t.Context())
	tempDir := t.TempDir()
	t.Setenv("FAKE_CLIENT", "true")

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
		ControlPlaneMIs:   &hyperv1.AzureResourceManagedIdentities{},
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
			name: "minimal flags necessary to render",
			args: []string{
				"--azure-creds=" + credentialsFile,
				"--infra-json=" + infraFile,
				"--rhcos-image=whatever",
				"--render-sensitive",
				"--name=example",
				"--pull-secret=" + pullSecretFile,
				"--managed-identities-file", filepath.Join(tempDir, "managedIdentities.json"),
				"--data-plane-identities-file", filepath.Join(tempDir, "dataPlaneIdentities.json"),
			},
		},
		{
			name: "complicated invocation from bryan",
			args: []string{
				"--azure-creds=" + credentialsFile,
				"--infra-json=" + infraFile,
				"--rhcos-image=whatever",
				"--name=bryans-cluster",
				"--location=eastus",
				"--node-pool-replicas=312",
				"--base-domain=base.domain.com",
				"--release-image=fake-release-image",
				"--enable-ephemeral-disk=true",
				"--instance-type=Standard_DS2_v2",
				"--disk-storage-account-type=Standard_LRS",
				"--render-sensitive",
				"--pull-secret=" + pullSecretFile,
				"--managed-identities-file", filepath.Join(tempDir, "managedIdentities.json"),
				"--data-plane-identities-file", filepath.Join(tempDir, "dataPlaneIdentities.json"),
			},
		},
		{
			name: "create with azure marketplace image",
			args: []string{
				"--azure-creds=" + credentialsFile,
				"--infra-json=" + infraFile,
				"--name=bryans-cluster",
				"--location=eastus",
				"--node-pool-replicas=312",
				"--base-domain=base.domain.com",
				"--release-image=fake-release-image",
				"--enable-ephemeral-disk=true",
				"--instance-type=Standard_DS2_v2",
				"--disk-storage-account-type=Standard_LRS",
				"--marketplace-publisher=azureopenshift",
				"--marketplace-offer=aro4",
				"--marketplace-sku=aro_414",
				"--marketplace-version=414.92.2024021",
				"--pull-secret=" + pullSecretFile,
				"--managed-identities-file", filepath.Join(tempDir, "managedIdentities.json"),
				"--data-plane-identities-file", filepath.Join(tempDir, "dataPlaneIdentities.json"),
			},
		},
		{
			name: "with availability zones",
			args: []string{
				"--azure-creds=" + credentialsFile,
				"--infra-json=" + infraFile,
				"--rhcos-image=whatever",
				"--render-sensitive",
				"--availability-zones=1,2",
				"--name=example",
				"--pull-secret=" + pullSecretFile,
				"--managed-identities-file", filepath.Join(tempDir, "managedIdentities.json"),
				"--data-plane-identities-file", filepath.Join(tempDir, "dataPlaneIdentities.json"),
			},
		},
		{
			name: "with disabled capabilities",
			args: []string{
				"--name=example",
				"--pull-secret=" + pullSecretFile,
				"--azure-creds=" + credentialsFile,
				"--infra-json=" + infraFile,
				"--rhcos-image=whatever",
				"--render-sensitive",
				"--managed-identities-file", filepath.Join(tempDir, "managedIdentities.json"),
				"--data-plane-identities-file", filepath.Join(tempDir, "dataPlaneIdentities.json"),
				"--disable-cluster-capabilities=ImageRegistry",
			},
		},
		{
			name: "with KubeAPIServerDNSName",
			args: []string{
				"--name=example",
				"--pull-secret=" + pullSecretFile,
				"--azure-creds=" + credentialsFile,
				"--infra-json=" + infraFile,
				"--rhcos-image=whatever",
				"--render-sensitive",
				"--managed-identities-file", filepath.Join(tempDir, "managedIdentities.json"),
				"--data-plane-identities-file", filepath.Join(tempDir, "dataPlaneIdentities.json"),
				"--kas-dns-name=test-dns-name.example.com",
			},
		},
		{
			name: "with image generation Gen1",
			args: []string{
				"--azure-creds=" + credentialsFile,
				"--infra-json=" + infraFile,
				"--name=example",
				"--pull-secret=" + pullSecretFile,
				"--managed-identities-file", filepath.Join(tempDir, "managedIdentities.json"),
				"--data-plane-identities-file", filepath.Join(tempDir, "dataPlaneIdentities.json"),
				"--image-generation=Gen1",
			},
		},
		{
			name: "with image generation Gen2",
			args: []string{
				"--azure-creds=" + credentialsFile,
				"--infra-json=" + infraFile,
				"--name=example",
				"--pull-secret=" + pullSecretFile,
				"--managed-identities-file", filepath.Join(tempDir, "managedIdentities.json"),
				"--data-plane-identities-file", filepath.Join(tempDir, "dataPlaneIdentities.json"),
				"--image-generation=Gen2",
			},
		},
		{
			name: "with marketplace flags and image generation Gen1",
			args: []string{
				"--azure-creds=" + credentialsFile,
				"--infra-json=" + infraFile,
				"--name=example",
				"--pull-secret=" + pullSecretFile,
				"--managed-identities-file", filepath.Join(tempDir, "managedIdentities.json"),
				"--data-plane-identities-file", filepath.Join(tempDir, "dataPlaneIdentities.json"),
				"--marketplace-publisher=azureopenshift",
				"--marketplace-offer=aro4",
				"--marketplace-sku=aro_414",
				"--marketplace-version=414.92.2024021",
				"--image-generation=Gen1",
			},
		},
		{
			name: "with availability zones and image generation Gen1",
			args: []string{
				"--azure-creds=" + credentialsFile,
				"--infra-json=" + infraFile,
				"--name=example",
				"--pull-secret=" + pullSecretFile,
				"--managed-identities-file", filepath.Join(tempDir, "managedIdentities.json"),
				"--data-plane-identities-file", filepath.Join(tempDir, "dataPlaneIdentities.json"),
				"--availability-zones=1,2,3",
				"--image-generation=Gen1",
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			flags := pflag.NewFlagSet(testCase.name, pflag.ContinueOnError)
			coreOpts := core.DefaultOptions()
			core.BindDeveloperOptions(coreOpts, flags)
			azureOpts, err := DefaultOptions()
			if err != nil {
				t.Fatal("failed to create azure options: ", err)
			}
			azurenodepool.BindOptions(azureOpts.NodePoolOpts, flags)
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
