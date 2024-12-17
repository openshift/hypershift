package powervs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/openshift/hypershift/cmd/cluster/core"
	powervsinfra "github.com/openshift/hypershift/cmd/infra/powervs"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/test/integration/framework"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	utilrand "k8s.io/apimachinery/pkg/util/rand"

	"github.com/spf13/pflag"
)

func TestCreateCluster(t *testing.T) {
	utilrand.Seed(1234567890)
	certs.UnsafeSeed(1234567890)
	ctx := framework.InterruptableContext(context.Background())
	tempDir := t.TempDir()
	t.Setenv("FAKE_CLIENT", "true")

	rawInfra, err := json.Marshal(&powervsinfra.Infra{
		ID:                "fakeID",
		Region:            "fakeRegion",
		Zone:              "fakeZone",
		VPCRegion:         "fakeVPCRegion",
		AccountID:         "fakeAccountID",
		BaseDomain:        "fakeBaseDomain",
		CISCRN:            "fakeCISCRN",
		CISDomainID:       "fakeCISDomainID",
		ResourceGroup:     "fakeResourceGroup",
		ResourceGroupID:   "fakeResourceGroupID",
		CloudInstanceID:   "fakeCloudInstanceID",
		DHCPSubnet:        "fakeDHCPSubnet",
		DHCPSubnetID:      "fakeDHCPSubnetID",
		DHCPID:            "fakeDHCPID",
		CloudConnectionID: "fakeCloudConnectionID",
		VPCName:           "fakeVPCName",
		VPCID:             "fakeVPCID",
		VPCCRN:            "fakeVPCCRN",
		VPCRoutingTableID: "fakeVPCRoutingTableID",
		VPCSubnetName:     "fakeVPCSubnetName",
		VPCSubnetID:       "fakeVPCSubnetID",
		Stats: powervsinfra.InfraCreationStat{
			VPC:                 powervsinfra.CreateStat{Duration: powervsinfra.TimeDuration{Duration: 0}, Status: "a"},
			VPCSubnet:           powervsinfra.CreateStat{Duration: powervsinfra.TimeDuration{Duration: 1}, Status: "b"},
			CloudInstance:       powervsinfra.CreateStat{Duration: powervsinfra.TimeDuration{Duration: 2}, Status: "c"},
			DHCPService:         powervsinfra.CreateStat{Duration: powervsinfra.TimeDuration{Duration: 3}, Status: "d"},
			CloudConnState:      powervsinfra.CreateStat{Duration: powervsinfra.TimeDuration{Duration: 4}, Status: "e"},
			TransitGatewayState: powervsinfra.CreateStat{Duration: powervsinfra.TimeDuration{Duration: 5}, Status: "f"},
		},
		Secrets: powervsinfra.Secrets{
			KubeCloudControllerManager: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "KubeCloudControllerManager"},
			},
			NodePoolManagement: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "NodePoolManagement"},
			},
			IngressOperator: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "IngressOperator"},
			},
			StorageOperator: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "StorageOperator"},
			},
			ImageRegistryOperator: &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "ImageRegistryOperator"},
			},
		},
		CloudInstanceCRN:       "fakeCloudInstanceCRN",
		TransitGatewayLocation: "fakeTransitGatewayLocation",
		TransitGatewayID:       "fakeTransitGatewayID",
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
				"--infra-json=" + infraFile,
				"--render-sensitive",
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			flags := pflag.NewFlagSet(testCase.name, pflag.ContinueOnError)
			coreOpts := core.DefaultOptions()
			core.BindDeveloperOptions(coreOpts, flags)
			powerVSOpts := DefaultOptions()
			BindOptions(powerVSOpts, flags)
			if err := flags.Parse(testCase.args); err != nil {
				t.Fatalf("failed to parse flags: %v", err)
			}

			tempDir := t.TempDir()
			manifestsFile := filepath.Join(tempDir, "manifests.yaml")
			coreOpts.Render = true
			coreOpts.RenderInto = manifestsFile

			if err := core.CreateCluster(ctx, coreOpts, powerVSOpts); err != nil {
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
