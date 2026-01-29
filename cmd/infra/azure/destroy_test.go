package azure

import (
	"context"
	"testing"

	"github.com/openshift/hypershift/cmd/util"
	"github.com/openshift/hypershift/support/testutil"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"

	"github.com/go-logr/logr"
)

func TestGetResourceGroupName(t *testing.T) {
	tests := []struct {
		name     string
		opts     DestroyInfraOptions
		expected string
	}{
		{
			name: "custom resource group name",
			opts: DestroyInfraOptions{
				Name:              "test-cluster",
				InfraID:           "abc123",
				ResourceGroupName: "custom-rg-name",
			},
			expected: "custom-rg-name",
		},
		{
			name: "default resource group name",
			opts: DestroyInfraOptions{
				Name:              "test-cluster",
				InfraID:           "abc123",
				ResourceGroupName: "",
			},
			expected: "test-cluster-abc123",
		},
		{
			name: "empty custom resource group name uses default",
			opts: DestroyInfraOptions{
				Name:              "my-cluster",
				InfraID:           "xyz789",
				ResourceGroupName: "",
			},
			expected: "my-cluster-xyz789",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.opts.GetResourceGroupName()
			if result != tt.expected {
				t.Errorf("GetResourceGroupName() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

func TestDestroyInfraRun(t *testing.T) {
	tests := []struct {
		name        string
		transport   policy.Transporter
		expectError bool
	}{
		{
			name:        "When resource group exists it should delete successfully",
			transport:   testutil.NewAzureResourceGroupSuccessTransport(),
			expectError: false,
		},
		{
			name:        "When resource group is not found (404) it should succeed without error",
			transport:   testutil.NewAzureResourceGroupNotFoundTransport(),
			expectError: false,
		},
		{
			name:        "When Azure returns authorization error (403) it should return an error",
			transport:   testutil.NewAzureForbiddenTransport(),
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &DestroyInfraOptions{
				Name:              "test-cluster",
				InfraID:           "abc123",
				ResourceGroupName: "test-rg",
				Cloud:             "AzurePublicCloud",
				Credentials: &util.AzureCreds{
					SubscriptionID: "test-subscription-id",
				},
				azureCredential: &testutil.FakeAzureCredential{},
				clientOptions: &arm.ClientOptions{
					ClientOptions: azcore.ClientOptions{
						Cloud:     cloud.AzurePublic,
						Transport: tt.transport,
					},
				},
			}

			err := opts.Run(context.Background(), logr.Discard())

			if tt.expectError && err == nil {
				t.Error("Expected an error but got nil")
			}
			if !tt.expectError && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}
		})
	}
}
