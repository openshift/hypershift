package azure

import (
	"strings"
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/nodepool/core"
	"github.com/openshift/hypershift/support/testutil"

	"k8s.io/utils/ptr"

	"github.com/spf13/pflag"
)

// Test data constants
const (
	testSubscriptionID       = "test"
	testResourceGroup        = "test"
	testVnetName             = "test"
	testSubnetName           = "test"
	testMarketplacePublisher = "redhat"
	testMarketplaceOffer     = "aro4"
	testMarketplaceSKU       = "aro_414"
	testMarketplaceVersion   = "414.1.20240101"
	testInstanceType         = "Standard_D4s_v5"
	testDiskStorageType      = "Premium_LRS"
)

var testSubnetID = "/subscriptions/" + testSubscriptionID + "/resourceGroups/" + testResourceGroup +
	"/providers/Microsoft.Network/virtualNetworks/" + testVnetName + "/subnets/" + testSubnetName

// TestCreateNodePool_When_flags_are_parsed_it_should_generate_correct_nodepool tests the full CLI flag parsing → Validate() → Complete() → NodePool manifest generation flow.
func TestCreateNodePool_When_flags_are_parsed_it_should_generate_correct_nodepool(t *testing.T) {
	for _, testCase := range []struct {
		name string
		args []string
	}{
		{
			name: "minimal configuration",
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
			name: "full configuration with Gen2",
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
				"--enable-ephemeral-disk=true",
				"--encryption-at-host=Enabled",
			},
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := t.Context()

			// Setup flag parsing
			flags := pflag.NewFlagSet(testCase.name, pflag.ContinueOnError)
			coreOpts := &core.CreateNodePoolOptions{
				Name:        "test-nodepool",
				Namespace:   "clusters",
				ClusterName: "test-cluster",
				Replicas:    3,
				Arch:        string(hyperv1.ArchitectureAMD64),
			}
			azureOpts := DefaultOptions()

			// Bind flags
			BindDeveloperOptions(azureOpts, flags)

			// Parse flags
			if err := flags.Parse(testCase.args); err != nil {
				t.Fatalf("failed to parse flags: %v", err)
			}

			// Validate
			validOpts, err := azureOpts.Validate(ctx, coreOpts)
			if err != nil {
				t.Fatalf("validation failed: %v", err)
			}

			// Complete
			completedOpts, err := validOpts.Complete(ctx, coreOpts)
			if err != nil {
				t.Fatalf("completion failed: %v", err)
			}

			// Generate NodePool
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

			// Compare with fixture
			testutil.CompareWithFixture(t, nodePool.Spec.Platform.Azure)
		})
	}
}

func TestNodePoolPlatformImageGeneration(t *testing.T) {
	testCases := []struct {
		name                    string
		imageGeneration         string
		nodePoolArch            string
		expectedImageGeneration *hyperv1.AzureVMImageGeneration
	}{
		{
			name:                    "Gen1 specified with AMD64",
			imageGeneration:         "Gen1",
			nodePoolArch:            string(hyperv1.ArchitectureAMD64),
			expectedImageGeneration: ptr.To(hyperv1.Gen1),
		},
		{
			name:                    "Gen2 specified with AMD64",
			imageGeneration:         "Gen2",
			nodePoolArch:            string(hyperv1.ArchitectureAMD64),
			expectedImageGeneration: ptr.To(hyperv1.Gen2),
		},
		{
			name:                    "Gen1 specified with ARM64",
			imageGeneration:         "Gen1",
			nodePoolArch:            string(hyperv1.ArchitectureARM64),
			expectedImageGeneration: ptr.To(hyperv1.Gen1),
		},
		{
			name:                    "Gen2 specified with ARM64",
			imageGeneration:         "Gen2",
			nodePoolArch:            string(hyperv1.ArchitectureARM64),
			expectedImageGeneration: ptr.To(hyperv1.Gen2),
		},
		{
			name:                    "No generation specified with AMD64",
			imageGeneration:         "",
			nodePoolArch:            string(hyperv1.ArchitectureAMD64),
			expectedImageGeneration: nil,
		},
		{
			name:                    "No generation specified with ARM64",
			imageGeneration:         "",
			nodePoolArch:            string(hyperv1.ArchitectureARM64),
			expectedImageGeneration: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			opts := &CompletedAzurePlatformCreateOptions{
				completetedAzurePlatformCreateOptions: &completetedAzurePlatformCreateOptions{
					AzurePlatformCreateOptions: &AzurePlatformCreateOptions{
						ImageGeneration:        tc.imageGeneration,
						InstanceType:           testInstanceType,
						SubnetID:               testSubnetID,
						DiskStorageAccountType: testDiskStorageType,
					},
					AzureMarketPlaceImageInfo: &AzureMarketPlaceImageInfo{
						MarketplacePublisher: testMarketplacePublisher,
						MarketplaceOffer:     testMarketplaceOffer,
						MarketplaceSKU:       testMarketplaceSKU,
						MarketplaceVersion:   testMarketplaceVersion,
					},
				},
			}

			nodePool := &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Arch: tc.nodePoolArch,
				},
			}

			platform := opts.NodePoolPlatform(nodePool)

			// Verify marketplace image is set correctly
			if platform.Image.Type != hyperv1.AzureMarketplace {
				t.Errorf("Expected image type to be %v, got %v", hyperv1.AzureMarketplace, platform.Image.Type)
			}

			if platform.Image.AzureMarketplace == nil {
				t.Errorf("Expected AzureMarketplace to be set")
			} else {
				if platform.Image.AzureMarketplace.Publisher != "redhat" {
					t.Errorf("Expected publisher to be 'redhat', got %v", platform.Image.AzureMarketplace.Publisher)
				}

				// Verify ImageGeneration is now inside AzureMarketplace struct
				if tc.expectedImageGeneration == nil {
					if platform.Image.AzureMarketplace.ImageGeneration != nil {
						t.Errorf("Expected ImageGeneration to be nil, got %v", *platform.Image.AzureMarketplace.ImageGeneration)
					}
				} else {
					if platform.Image.AzureMarketplace.ImageGeneration == nil {
						t.Errorf("Expected ImageGeneration to be %v, got nil", *tc.expectedImageGeneration)
					} else if *platform.Image.AzureMarketplace.ImageGeneration != *tc.expectedImageGeneration {
						t.Errorf("Expected ImageGeneration to be %v, got %v", *tc.expectedImageGeneration, *platform.Image.AzureMarketplace.ImageGeneration)
					}
				}
			}
		})
	}
}

func TestValidateImageGeneration(t *testing.T) {
	testCases := []struct {
		name          string
		imageGen      string
		shouldError   bool
		expectedError string
	}{
		{
			name:        "Valid Gen1",
			imageGen:    "Gen1",
			shouldError: false,
		},
		{
			name:        "Valid Gen2",
			imageGen:    "Gen2",
			shouldError: false,
		},
		{
			name:        "Empty is valid",
			imageGen:    "",
			shouldError: false,
		},
		{
			name:          "Invalid Gen3",
			imageGen:      "Gen3",
			shouldError:   true,
			expectedError: "invalid value for --image-generation: Gen3. Supported values: Gen1, Gen2",
		},
		{
			name:          "Invalid lowercase",
			imageGen:      "gen1",
			shouldError:   true,
			expectedError: "invalid value for --image-generation: gen1. Supported values: Gen1, Gen2",
		},
		{
			name:          "Invalid upper case",
			imageGen:      "GEN1",
			shouldError:   true,
			expectedError: "invalid value for --image-generation: GEN1. Supported values: Gen1, Gen2",
		},
		{
			name:          "Invalid numeric",
			imageGen:      "1",
			shouldError:   true,
			expectedError: "invalid value for --image-generation: 1. Supported values: Gen1, Gen2",
		},
		{
			name:          "Invalid random string",
			imageGen:      "invalid",
			shouldError:   true,
			expectedError: "invalid value for --image-generation: invalid. Supported values: Gen1, Gen2",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			opts := DefaultOptions()
			opts.ImageGeneration = tc.imageGen
			opts.MarketplacePublisher = testMarketplacePublisher
			opts.MarketplaceOffer = testMarketplaceOffer
			opts.MarketplaceSKU = testMarketplaceSKU
			opts.MarketplaceVersion = testMarketplaceVersion
			opts.InstanceType = testInstanceType
			opts.SubnetID = testSubnetID

			_, err := opts.Validate(t.Context(), nil)

			if tc.shouldError {
				if err == nil {
					t.Errorf("Expected validation to fail, but it passed")
				} else if err.Error() != tc.expectedError {
					t.Errorf("Expected error '%s', got '%s'", tc.expectedError, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Expected validation to pass, but got error: %v", err)
				}
			}
		})
	}
}

func TestAzureBoundaryConditions(t *testing.T) {
	testCases := []struct {
		name          string
		modifyOpts    func(*RawAzurePlatformCreateOptions)
		shouldError   bool
		expectedError string
	}{
		{
			name: "valid minimal configuration",
			modifyOpts: func(opts *RawAzurePlatformCreateOptions) {
				// No modifications - should be valid
			},
			shouldError: false,
		},
		{
			name: "whitespace only image generation",
			modifyOpts: func(opts *RawAzurePlatformCreateOptions) {
				opts.ImageGeneration = "  "
			},
			shouldError:   true,
			expectedError: "invalid value for --image-generation",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			opts := DefaultOptions()
			opts.ImageGeneration = "Gen2"
			opts.MarketplacePublisher = testMarketplacePublisher
			opts.MarketplaceOffer = testMarketplaceOffer
			opts.MarketplaceSKU = testMarketplaceSKU
			opts.MarketplaceVersion = testMarketplaceVersion
			opts.InstanceType = testInstanceType
			opts.SubnetID = testSubnetID

			// Apply test-specific modifications
			tc.modifyOpts(opts)

			_, err := opts.Validate(t.Context(), nil)

			if tc.shouldError {
				if err == nil {
					t.Errorf("Expected validation to fail, but it passed")
				} else if tc.expectedError != "" && !strings.Contains(err.Error(), tc.expectedError) {
					t.Errorf("Expected error to contain '%s', got '%s'", tc.expectedError, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Expected validation to pass, but got error: %v", err)
				}
			}
		})
	}
}
