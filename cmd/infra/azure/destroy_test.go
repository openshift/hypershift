package azure

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"

	"github.com/go-logr/logr"
)

// mockResourceDeleter implements the resourceDeleter interface for testing.
type mockResourceDeleter struct {
	listFunc   func(ctx context.Context, rg string) ([]resourceToDelete, error)
	deleteFunc func(ctx context.Context, id string, apiVersion string) error
}

func (m *mockResourceDeleter) ListByResourceGroup(ctx context.Context, rg string) ([]resourceToDelete, error) {
	return m.listFunc(ctx, rg)
}

func (m *mockResourceDeleter) DeleteByID(ctx context.Context, id string, apiVersion string) error {
	return m.deleteFunc(ctx, id, apiVersion)
}

func TestDeleteClusterResourcesInGroup(t *testing.T) {
	t.Parallel()

	t.Run("When all deletions succeed on first pass it should return no error", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		mock := &mockResourceDeleter{
			listFunc: func(_ context.Context, _ string) ([]resourceToDelete, error) {
				return []resourceToDelete{
					{id: "/sub/rg/dns-zone-1", apiVersion: "2020-06-01", name: "zone-abc123", resourceType: "Microsoft.Network/privateDnsZones"},
				}, nil
			},
			deleteFunc: func(_ context.Context, _ string, _ string) error {
				return nil
			},
		}

		opts := &DestroyInfraOptions{
			Name:    "test-cluster",
			InfraID: "abc123",
		}

		err := opts.deleteClusterResourcesInGroup(context.Background(), logr.Discard(), mock, "test-rg")
		g.Expect(err).ToNot(HaveOccurred())
	})

	t.Run("When a deletion fails it should return an error", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		mock := &mockResourceDeleter{
			listFunc: func(_ context.Context, _ string) ([]resourceToDelete, error) {
				return []resourceToDelete{
					{id: "/sub/rg/dns-zone-1", apiVersion: "2020-06-01", name: "zone-abc123", resourceType: "Microsoft.Network/privateDnsZones"},
				}, nil
			},
			deleteFunc: func(_ context.Context, _ string, _ string) error {
				return fmt.Errorf("deletion failed: child resources still exist")
			},
		}

		opts := &DestroyInfraOptions{
			Name:    "test-cluster",
			InfraID: "abc123",
		}

		err := opts.deleteClusterResourcesInGroup(context.Background(), logr.Discard(), mock, "test-rg")
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("child resources still exist"))
	})

	t.Run("When a resource is not found it should skip and continue", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		mock := &mockResourceDeleter{
			listFunc: func(_ context.Context, _ string) ([]resourceToDelete, error) {
				return []resourceToDelete{
					{id: "/sub/rg/zone-1", apiVersion: "2020-06-01", name: "zone-abc123", resourceType: "Microsoft.Network/privateDnsZones"},
					{id: "/sub/rg/zone-2", apiVersion: "2020-06-01", name: "zone2-abc123", resourceType: "Microsoft.Network/privateDnsZones"},
				}, nil
			},
			deleteFunc: func(_ context.Context, id string, _ string) error {
				if id == "/sub/rg/zone-1" {
					return &azcore.ResponseError{
						ErrorCode:  "ResourceNotFound",
						StatusCode: http.StatusNotFound,
					}
				}
				return nil
			},
		}

		opts := &DestroyInfraOptions{
			Name:    "test-cluster",
			InfraID: "abc123",
		}

		err := opts.deleteClusterResourcesInGroup(context.Background(), logr.Discard(), mock, "test-rg")
		g.Expect(err).ToNot(HaveOccurred())
	})

	t.Run("When multiple deletions fail it should return all errors", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		mock := &mockResourceDeleter{
			listFunc: func(_ context.Context, _ string) ([]resourceToDelete, error) {
				return []resourceToDelete{
					{id: "/sub/rg/zone-1", apiVersion: "2020-06-01", name: "zone-abc123", resourceType: "Microsoft.Network/privateDnsZones"},
					{id: "/sub/rg/zone-2", apiVersion: "2020-06-01", name: "zone2-abc123", resourceType: "Microsoft.Network/privateDnsZones"},
				}, nil
			},
			deleteFunc: func(_ context.Context, id string, _ string) error {
				if id == "/sub/rg/zone-1" {
					return fmt.Errorf("first deletion failed")
				}
				return fmt.Errorf("second deletion failed")
			},
		}

		opts := &DestroyInfraOptions{
			Name:    "test-cluster",
			InfraID: "abc123",
		}

		err := opts.deleteClusterResourcesInGroup(context.Background(), logr.Discard(), mock, "test-rg")
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("first deletion failed"))
		g.Expect(err.Error()).To(ContainSubstring("second deletion failed"))
	})

	t.Run("When a resource matches the azurecluster prefix it should be deleted", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		var deletedIDs []string
		mock := &mockResourceDeleter{
			listFunc: func(_ context.Context, _ string) ([]resourceToDelete, error) {
				return []resourceToDelete{
					{id: "/sub/rg/azurecluster-resource", apiVersion: "2020-06-01", name: "test-cluster-azurecluster.example.com", resourceType: "Microsoft.Network/privateDnsZones"},
				}, nil
			},
			deleteFunc: func(_ context.Context, id string, _ string) error {
				deletedIDs = append(deletedIDs, id)
				return nil
			},
		}

		opts := &DestroyInfraOptions{
			Name:    "test-cluster",
			InfraID: "xyz999",
		}

		err := opts.deleteClusterResourcesInGroup(context.Background(), logr.Discard(), mock, "test-rg")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(deletedIDs).To(ContainElement("/sub/rg/azurecluster-resource"))
	})

	t.Run("When listing resources fails it should return the error", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		mock := &mockResourceDeleter{
			listFunc: func(_ context.Context, _ string) ([]resourceToDelete, error) {
				return nil, fmt.Errorf("failed to list resources")
			},
			deleteFunc: func(_ context.Context, _ string, _ string) error {
				t.Fatal("deleteFunc should not be called")
				return nil
			},
		}

		opts := &DestroyInfraOptions{
			Name:    "test-cluster",
			InfraID: "abc123",
		}

		err := opts.deleteClusterResourcesInGroup(context.Background(), logr.Discard(), mock, "test-rg")
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("failed to list resources"))
	})

	t.Run("When a resource does not match cluster patterns it should be preserved", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		mock := &mockResourceDeleter{
			listFunc: func(_ context.Context, _ string) ([]resourceToDelete, error) {
				return []resourceToDelete{
					{id: "/sub/rg/other-resource", apiVersion: "2020-06-01", name: "unrelated-resource", resourceType: "Microsoft.Network/privateDnsZones"},
				}, nil
			},
			deleteFunc: func(_ context.Context, _ string, _ string) error {
				t.Fatal("deleteFunc should not be called for non-cluster resources")
				return nil
			},
		}

		opts := &DestroyInfraOptions{
			Name:    "test-cluster",
			InfraID: "abc123",
		}

		err := opts.deleteClusterResourcesInGroup(context.Background(), logr.Discard(), mock, "test-rg")
		g.Expect(err).ToNot(HaveOccurred())
	})
}

func TestRetryDeleteClusterResources(t *testing.T) {
	t.Parallel()

	t.Run("When DNS zone deletion fails on first pass it should succeed on retry", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		callCount := 0
		mock := &mockResourceDeleter{
			listFunc: func(_ context.Context, _ string) ([]resourceToDelete, error) {
				callCount++
				if callCount == 1 {
					return []resourceToDelete{
						{id: "/sub/rg/vnetlink-1", apiVersion: "2020-06-01", name: "link-abc123", resourceType: "Microsoft.Network/privateDnsZones/virtualNetworkLinks"},
						{id: "/sub/rg/dns-zone-1", apiVersion: "2020-06-01", name: "zone-abc123", resourceType: "Microsoft.Network/privateDnsZones"},
					}, nil
				}
				// Second pass: vnet link is gone, only zone remains
				return []resourceToDelete{
					{id: "/sub/rg/dns-zone-1", apiVersion: "2020-06-01", name: "zone-abc123", resourceType: "Microsoft.Network/privateDnsZones"},
				}, nil
			},
			deleteFunc: func(_ context.Context, id string, _ string) error {
				if id == "/sub/rg/dns-zone-1" && callCount == 1 {
					return fmt.Errorf("child resources still exist")
				}
				return nil
			},
		}

		opts := &DestroyInfraOptions{
			Name:                  "test-cluster",
			InfraID:               "abc123",
			AzureInfraGracePeriod: 5 * time.Second,
			retryInterval:         100 * time.Millisecond,
		}

		err := opts.retryDeleteClusterResources(context.Background(), logr.Discard(), mock, "test-rg")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(callCount).To(BeNumerically(">=", 2))
	})

	t.Run("When a non-retriable error occurs it should stop retrying immediately", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		callCount := 0
		mock := &mockResourceDeleter{
			listFunc: func(_ context.Context, _ string) ([]resourceToDelete, error) {
				callCount++
				return []resourceToDelete{
					{id: "/sub/rg/zone-1", apiVersion: "2020-06-01", name: "zone-abc123", resourceType: "Microsoft.Network/privateDnsZones"},
				}, nil
			},
			deleteFunc: func(_ context.Context, _ string, _ string) error {
				return &testNonRetriableError{msg: "authentication failed"}
			},
		}

		opts := &DestroyInfraOptions{
			Name:                  "test-cluster",
			InfraID:               "abc123",
			AzureInfraGracePeriod: 5 * time.Second,
			retryInterval:         100 * time.Millisecond,
		}

		err := opts.retryDeleteClusterResources(context.Background(), logr.Discard(), mock, "test-rg")
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("authentication failed"))
		g.Expect(callCount).To(Equal(1))
	})

	t.Run("When timeout is exceeded it should return a wrapped error with resource group name", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		mock := &mockResourceDeleter{
			listFunc: func(_ context.Context, _ string) ([]resourceToDelete, error) {
				return []resourceToDelete{
					{id: "/sub/rg/zone-1", apiVersion: "2020-06-01", name: "zone-abc123", resourceType: "Microsoft.Network/privateDnsZones"},
				}, nil
			},
			deleteFunc: func(_ context.Context, _ string, _ string) error {
				return fmt.Errorf("child resources still exist")
			},
		}

		opts := &DestroyInfraOptions{
			Name:                  "test-cluster",
			InfraID:               "abc123",
			AzureInfraGracePeriod: 500 * time.Millisecond,
			retryInterval:         100 * time.Millisecond,
		}

		err := opts.retryDeleteClusterResources(context.Background(), logr.Discard(), mock, "test-rg")
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("timed out"))
		g.Expect(err.Error()).To(ContainSubstring("test-rg"))
		g.Expect(errors.Is(err, context.DeadlineExceeded)).To(BeTrue())
	})

	t.Run("When Azure API returns 429 throttling it should retry", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		callCount := 0
		mock := &mockResourceDeleter{
			listFunc: func(_ context.Context, _ string) ([]resourceToDelete, error) {
				callCount++
				if callCount <= 2 {
					return []resourceToDelete{
						{id: "/sub/rg/zone-1", apiVersion: "2020-06-01", name: "zone-abc123", resourceType: "Microsoft.Network/privateDnsZones"},
					}, nil
				}
				return nil, nil
			},
			deleteFunc: func(_ context.Context, _ string, _ string) error {
				if callCount <= 1 {
					return &azcore.ResponseError{
						StatusCode: 429,
						ErrorCode:  "TooManyRequests",
					}
				}
				return nil
			},
		}

		opts := &DestroyInfraOptions{
			Name:                  "test-cluster",
			InfraID:               "abc123",
			AzureInfraGracePeriod: 5 * time.Second,
			retryInterval:         100 * time.Millisecond,
		}

		err := opts.retryDeleteClusterResources(context.Background(), logr.Discard(), mock, "test-rg")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(callCount).To(BeNumerically(">=", 2))
	})

	t.Run("When grace period is zero it should attempt deletion once without retries", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		callCount := 0
		mock := &mockResourceDeleter{
			listFunc: func(_ context.Context, _ string) ([]resourceToDelete, error) {
				callCount++
				return []resourceToDelete{
					{id: "/sub/rg/zone-1", apiVersion: "2020-06-01", name: "zone-abc123", resourceType: "Microsoft.Network/privateDnsZones"},
				}, nil
			},
			deleteFunc: func(_ context.Context, _ string, _ string) error {
				return nil
			},
		}

		opts := &DestroyInfraOptions{
			Name:                  "test-cluster",
			InfraID:               "abc123",
			AzureInfraGracePeriod: 0,
			retryInterval:         100 * time.Millisecond,
		}

		err := opts.retryDeleteClusterResources(context.Background(), logr.Discard(), mock, "test-rg")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(callCount).To(Equal(1))
	})

	t.Run("When retrying it should re-list resources on each pass", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		listCallCount := 0
		deleteCallCount := 0
		mock := &mockResourceDeleter{
			listFunc: func(_ context.Context, _ string) ([]resourceToDelete, error) {
				listCallCount++
				if listCallCount <= 2 {
					return []resourceToDelete{
						{id: "/sub/rg/zone-1", apiVersion: "2020-06-01", name: "zone-abc123", resourceType: "Microsoft.Network/privateDnsZones"},
					}, nil
				}
				return nil, nil
			},
			deleteFunc: func(_ context.Context, _ string, _ string) error {
				deleteCallCount++
				if deleteCallCount <= 2 {
					return fmt.Errorf("still deleting")
				}
				return nil
			},
		}

		opts := &DestroyInfraOptions{
			Name:                  "test-cluster",
			InfraID:               "abc123",
			AzureInfraGracePeriod: 5 * time.Second,
			retryInterval:         100 * time.Millisecond,
		}

		err := opts.retryDeleteClusterResources(context.Background(), logr.Discard(), mock, "test-rg")
		g.Expect(err).ToNot(HaveOccurred())
		g.Expect(listCallCount).To(BeNumerically(">=", 2))
	})
}

// testNonRetriableError implements the nonRetriableError interface for testing.
var _ nonRetriableError = (*testNonRetriableError)(nil)

type testNonRetriableError struct {
	msg string
}

func (e *testNonRetriableError) Error() string { return e.msg }
func (e *testNonRetriableError) NonRetriable() {}

func TestSortResourcesByDeletionOrder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		resources     []resourceToDelete
		expectedOrder []string // expected order of resource types
	}{
		{
			name: "When mixed resource types are provided it should sort by deletion priority",
			resources: []resourceToDelete{
				{id: "1", name: "storage", resourceType: "Microsoft.Storage/storageAccounts"},
				{id: "2", name: "vnetlink", resourceType: "Microsoft.Network/privateDnsZones/virtualNetworkLinks"},
				{id: "3", name: "vm", resourceType: "Microsoft.Compute/virtualMachines"},
				{id: "4", name: "lb", resourceType: "Microsoft.Network/loadBalancers"},
				{id: "5", name: "nic", resourceType: "Microsoft.Network/networkInterfaces"},
			},
			expectedOrder: []string{
				"Microsoft.Network/privateDnsZones/virtualNetworkLinks", // 1
				"Microsoft.Compute/virtualMachines",                     // 2
				"Microsoft.Network/networkInterfaces",                   // 3
				"Microsoft.Network/loadBalancers",                       // 4
				"Microsoft.Storage/storageAccounts",                     // 11
			},
		},
		{
			name: "When network resources are provided it should put subnets before vnets and vnets before dns zones",
			resources: []resourceToDelete{
				{id: "1", name: "dns", resourceType: "Microsoft.Network/privateDnsZones"},
				{id: "2", name: "vnet", resourceType: "Microsoft.Network/virtualNetworks"},
				{id: "3", name: "nsg", resourceType: "Microsoft.Network/networkSecurityGroups"},
				{id: "4", name: "identity", resourceType: "Microsoft.ManagedIdentity/userAssignedIdentities"},
				{id: "5", name: "subnet", resourceType: "Microsoft.Network/virtualNetworks/subnets"},
			},
			expectedOrder: []string{
				"Microsoft.Network/virtualNetworks/subnets",        // 7
				"Microsoft.Network/networkSecurityGroups",          // 8
				"Microsoft.Network/virtualNetworks",                // 9
				"Microsoft.Network/privateDnsZones",                // 10
				"Microsoft.ManagedIdentity/userAssignedIdentities", // 12
			},
		},
		{
			name: "When unknown resource types are provided it should place them after known types",
			resources: []resourceToDelete{
				{id: "1", name: "unknown", resourceType: "Microsoft.Unknown/someResource"},
				{id: "2", name: "vm", resourceType: "Microsoft.Compute/virtualMachines"},
			},
			expectedOrder: []string{
				"Microsoft.Compute/virtualMachines", // 2
				"Microsoft.Unknown/someResource",    // 99
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			sortResourcesByDeletionOrder(tt.resources)

			for i, expected := range tt.expectedOrder {
				g.Expect(tt.resources[i].resourceType).To(Equal(expected))
			}
		})
	}
}

func TestGetAPIVersionForResourceType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		resourceType string
		expected     string
	}{
		{
			name:         "When resource type is public IP addresses it should return correct API version",
			resourceType: "Microsoft.Network/publicIPAddresses",
			expected:     "2023-11-01",
		},
		{
			name:         "When resource type is load balancers it should return correct API version",
			resourceType: "Microsoft.Network/loadBalancers",
			expected:     "2023-11-01",
		},
		{
			name:         "When resource type is network interfaces it should return correct API version",
			resourceType: "Microsoft.Network/networkInterfaces",
			expected:     "2023-11-01",
		},
		{
			name:         "When resource type is network security groups it should return correct API version",
			resourceType: "Microsoft.Network/networkSecurityGroups",
			expected:     "2023-11-01",
		},
		{
			name:         "When resource type is virtual networks it should return correct API version",
			resourceType: "Microsoft.Network/virtualNetworks",
			expected:     "2023-11-01",
		},
		{
			name:         "When resource type is private DNS zones it should return correct API version",
			resourceType: "Microsoft.Network/privateDnsZones",
			expected:     "2020-06-01",
		},
		{
			name:         "When resource type is private DNS zone virtual network links it should return correct API version",
			resourceType: "Microsoft.Network/privateDnsZones/virtualNetworkLinks",
			expected:     "2020-06-01",
		},
		{
			name:         "When resource type is virtual machines it should return correct API version",
			resourceType: "Microsoft.Compute/virtualMachines",
			expected:     "2024-03-01",
		},
		{
			name:         "When resource type is disks it should return correct API version",
			resourceType: "Microsoft.Compute/disks",
			expected:     "2023-10-02",
		},
		{
			name:         "When resource type is storage accounts it should return correct API version",
			resourceType: "Microsoft.Storage/storageAccounts",
			expected:     "2023-01-01",
		},
		{
			name:         "When resource type is user assigned identities it should return correct API version",
			resourceType: "Microsoft.ManagedIdentity/userAssignedIdentities",
			expected:     "2023-01-31",
		},
		{
			name:         "When resource type is unknown it should return default API version",
			resourceType: "Microsoft.Unknown/someResource",
			expected:     "2021-04-01",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			result := getAPIVersionForResourceType(tt.resourceType)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}

func TestDestroyValidate(t *testing.T) {
	t.Parallel()

	t.Run("When negative grace period is provided it should return an error", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		opts := &DestroyInfraOptions{
			Name:                  "test-cluster",
			InfraID:               "abc123",
			CredentialsFile:       "/tmp/creds",
			AzureInfraGracePeriod: -1 * time.Second,
		}

		err := opts.Validate()
		g.Expect(err).To(HaveOccurred())
		g.Expect(err.Error()).To(ContainSubstring("azure-infra-grace-period must be >= 0"))
	})

	t.Run("When zero grace period is provided it should not return an error", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)

		opts := &DestroyInfraOptions{
			Name:            "test-cluster",
			InfraID:         "abc123",
			CredentialsFile: "/tmp/creds",
		}

		err := opts.Validate()
		g.Expect(err).ToNot(HaveOccurred())
	})
}

func TestDestroyGetResourceGroupName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		opts     DestroyInfraOptions
		expected string
	}{
		{
			name: "When custom resource group name is provided it should use that name",
			opts: DestroyInfraOptions{
				Name:              "test-cluster",
				InfraID:           "abc123",
				ResourceGroupName: "custom-rg-name",
			},
			expected: "custom-rg-name",
		},
		{
			name: "When no resource group name is provided it should use default format",
			opts: DestroyInfraOptions{
				Name:              "test-cluster",
				InfraID:           "abc123",
				ResourceGroupName: "",
			},
			expected: "test-cluster-abc123",
		},
		{
			name: "When empty resource group name is provided it should use default format",
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
			t.Parallel()
			g := NewWithT(t)

			result := tt.opts.getResourceGroupName()
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}
