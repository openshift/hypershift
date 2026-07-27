package azure

import (
	"sort"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hypershiftazure "github.com/openshift/hypershift/cmd/nodepool/azure"
	"github.com/openshift/hypershift/cmd/nodepool/core"
	"github.com/openshift/hypershift/support/testutil"

	"github.com/spf13/pflag"
)

const (
	testSubscriptionID       = "12345678-1234-1234-1234-123456789abc"
	testResourceGroup        = "my-rg"
	testVnetName             = "my-vnet"
	testSubnetName           = "my-subnet"
	testMarketplacePublisher = "redhat"
	testMarketplaceOffer     = "aro4"
	testMarketplaceSKU       = "aro_414"
	testMarketplaceVersion   = "414.1.20240101"
	testInstanceType         = "Standard_D4s_v5"
)

var testSubnetID = "/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroup +
	"/providers/Microsoft.Network/virtualNetworks/" + testVnetName + "/subnets/" + testSubnetName

func TestNewCreateCommand(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		verify func(t *testing.T)
	}{
		"When Azure nodepool create command is created, it should have 'azure' as use": {
			verify: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewCreateCommand(&core.CreateNodePoolOptions{})
				g.Expect(cmd.Use).To(Equal("azure"))
			},
		},
		"When Azure nodepool create command is created, it should default root-disk-size to 120": {
			verify: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewCreateCommand(&core.CreateNodePoolOptions{})
				g.Expect(cmd.Flag("root-disk-size").DefValue).To(Equal("120"))
			},
		},
		"When Azure nodepool create command is created, it should default disk-storage-account-type to Premium_LRS": {
			verify: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewCreateCommand(&core.CreateNodePoolOptions{})
				g.Expect(cmd.Flag("disk-storage-account-type").DefValue).To(Equal("Premium_LRS"))
			},
		},
		"When Azure nodepool create command is created, it should register exactly the expected flags": {
			verify: func(t *testing.T) {
				g := NewWithT(t)
				cmd := NewCreateCommand(&core.CreateNodePoolOptions{})
				expectedFlags := []string{
					"availability-zone",
					"diagnostics-storage-account-type",
					"diagnostics-storage-account-uri",
					"disk-encryption-set-id",
					"disk-storage-account-type",
					"enable-ephemeral-disk",
					"encryption-at-host",
					"image-generation",
					"instance-type",
					"marketplace-offer",
					"marketplace-publisher",
					"marketplace-sku",
					"marketplace-version",
					"nodepool-subnet-id",
					"root-disk-size",
				}
				var actualFlags []string
				cmd.Flags().VisitAll(func(f *pflag.Flag) {
					actualFlags = append(actualFlags, f.Name)
				})
				sort.Strings(actualFlags)
				g.Expect(actualFlags).To(Equal(expectedFlags))
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			test.verify(t)
		})
	}
}

func TestUpdateNodePool(t *testing.T) {
	for _, testCase := range []struct {
		name string
		args []string
	}{
		{
			name: "When minimal configuration is provided, it should generate correct nodepool",
			args: []string{
				"--instance-type=" + testInstanceType,
				"--nodepool-subnet-id=" + testSubnetID,
				"--marketplace-publisher=" + testMarketplacePublisher,
				"--marketplace-offer=" + testMarketplaceOffer,
				"--marketplace-sku=" + testMarketplaceSKU,
				"--marketplace-version=" + testMarketplaceVersion,
			},
		},
		{
			name: "When full configuration is provided, it should generate correct nodepool",
			args: []string{
				"--instance-type=Standard_D8s_v5",
				"--nodepool-subnet-id=" + testSubnetID,
				"--marketplace-publisher=" + testMarketplacePublisher,
				"--marketplace-offer=" + testMarketplaceOffer,
				"--marketplace-sku=" + testMarketplaceSKU,
				"--marketplace-version=" + testMarketplaceVersion,
				"--image-generation=Gen2",
				"--root-disk-size=256",
				"--availability-zone=1",
				"--disk-storage-account-type=StandardSSD_LRS",
				"--disk-encryption-set-id=/subscriptions/test/resourceGroups/test/providers/Microsoft.Compute/diskEncryptionSets/test-des",
				"--enable-ephemeral-disk=true",
				"--encryption-at-host=Enabled",
				"--diagnostics-storage-account-type=UserManaged",
				"--diagnostics-storage-account-uri=https://testdiag.blob.core.windows.net",
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := t.Context()

			flags := pflag.NewFlagSet(testCase.name, pflag.ContinueOnError)
			coreOpts := &core.CreateNodePoolOptions{
				Name:        "test-nodepool",
				Namespace:   "clusters",
				ClusterName: "test-cluster",
				Replicas:    3,
				Arch:        string(hyperv1.ArchitectureAMD64),
			}
			azureOpts := hypershiftazure.DefaultOptions()
			hypershiftazure.BindProductFlags(azureOpts, flags)

			if err := flags.Parse(testCase.args); err != nil {
				t.Fatalf("failed to parse flags: %v", err)
			}

			validOpts, err := azureOpts.Validate(ctx, coreOpts)
			if err != nil {
				t.Fatalf("validation failed: %v", err)
			}

			completedOpts, err := validOpts.Complete(ctx, coreOpts)
			if err != nil {
				t.Fatalf("completion failed: %v", err)
			}

			nodePool := &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Arch: coreOpts.Arch,
					Platform: hyperv1.NodePoolPlatform{
						Type: hyperv1.AzurePlatform,
					},
				},
			}

			if err := completedOpts.UpdateNodePool(ctx, nodePool, nil, nil); err != nil {
				t.Fatalf("failed to update nodepool: %v", err)
			}

			testutil.CompareWithFixture(t, nodePool.Spec.Platform.Azure)
		})
	}
}
