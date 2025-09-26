package azure

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"k8s.io/utils/ptr"
)

func TestNodePoolPlatformImageGeneration(t *testing.T) {
	testCases := []struct {
		name                    string
		imageGeneration         string
		expectedImageGeneration *hyperv1.AzureVMImageGeneration
	}{
		{
			name:                    "Gen1 specified",
			imageGeneration:         "Gen1",
			expectedImageGeneration: ptr.To(hyperv1.Gen1),
		},
		{
			name:                    "Gen2 specified",
			imageGeneration:         "Gen2",
			expectedImageGeneration: ptr.To(hyperv1.Gen2),
		},
		{
			name:                    "No generation specified",
			imageGeneration:         "",
			expectedImageGeneration: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			opts := &CompletedAzurePlatformCreateOptions{
				completetedAzurePlatformCreateOptions: &completetedAzurePlatformCreateOptions{
					AzurePlatformCreateOptions: &AzurePlatformCreateOptions{
						ImageGeneration:      tc.imageGeneration,
						InstanceType:         "Standard_D4s_v3",
						SubnetID:             "/subscriptions/test/resourceGroups/test/providers/Microsoft.Network/virtualNetworks/test/subnets/test",
						DiskStorageAccountType: "Premium_LRS",
					},
					AzureMarketPlaceImageInfo: &AzureMarketPlaceImageInfo{
						MarketplacePublisher: "redhat",
						MarketplaceOffer:     "aro4",
						MarketplaceSKU:       "aro_414",
						MarketplaceVersion:   "414.1.20240101",
					},
				},
			}

			nodePool := &hyperv1.NodePool{
				Spec: hyperv1.NodePoolSpec{
					Arch: hyperv1.ArchitectureAMD64,
				},
			}

			platform := opts.NodePoolPlatform(nodePool)

			if tc.expectedImageGeneration == nil {
				if platform.Image.ImageGeneration != nil {
					t.Errorf("Expected ImageGeneration to be nil, got %v", *platform.Image.ImageGeneration)
				}
			} else {
				if platform.Image.ImageGeneration == nil {
					t.Errorf("Expected ImageGeneration to be %v, got nil", *tc.expectedImageGeneration)
				} else if *platform.Image.ImageGeneration != *tc.expectedImageGeneration {
					t.Errorf("Expected ImageGeneration to be %v, got %v", *tc.expectedImageGeneration, *platform.Image.ImageGeneration)
				}
			}

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
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			opts := DefaultOptions()
			opts.ImageGeneration = tc.imageGen
			opts.MarketplacePublisher = "redhat"
			opts.MarketplaceOffer = "aro4"
			opts.MarketplaceSKU = "aro_414"
			opts.MarketplaceVersion = "414.1.20240101"
			opts.InstanceType = "Standard_D4s_v3"
			opts.SubnetID = "/subscriptions/test/resourceGroups/test/providers/Microsoft.Network/virtualNetworks/test/subnets/test"

			_, err := opts.Validate()

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