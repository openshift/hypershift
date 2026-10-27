package azure

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/support/config"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"

	crclient "sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/go-logr/logr"
)

func TestCleanupRoleAssignments(t *testing.T) {
	t.Parallel()
	cleanupErr := errors.New("role assignment delete forbidden")
	tests := []struct {
		name       string
		nsgRG      string
		vnetRG     string
		cleanupErr error
		notify     bool
	}{
		{"When no overrides are provided, it should retain production defaults and log-only errors", "", "", cleanupErr, false},
		{"When CI provides scopes and cleanup fails, it should notify CI of the original error", "Shared-NSG", "Shared-VNet", cleanupErr, true},
		{"When cleanup succeeds, it should not notify CI of an error", "cluster-nsg-infra", "cluster-vnet-infra", nil, true},
		{"When cleanup is canceled, it should notify CI without changing the cleanup return contract", "Shared-NSG", "Shared-VNet", context.Canceled, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			opts := &core.DestroyOptions{Name: "cluster", InfraID: "infra", Log: logr.Discard(), AzurePlatform: core.AzurePlatformDestroyOptions{
				ResourceGroupName: "cluster-rg", DNSZoneRGName: "DNS-RG", NSGResourceGroupName: test.nsgRG, VnetResourceGroupName: test.vnetRG,
			}}
			var notified error
			if test.notify {
				opts.AzurePlatform.OnRoleAssignmentCleanupError = func(err error) { notified = err }
			}
			called := false
			cleanupRoleAssignments(t.Context(), opts, func(ctx context.Context, logger logr.Logger, infraID, clusterRG, nsgRG, vnetRG, dnsRG string, customRoles bool) error {
				called = true
				expectedNSG, expectedVnet := test.nsgRG, test.vnetRG
				if expectedNSG == "" {
					expectedNSG = "cluster-nsg"
				}
				if expectedVnet == "" {
					expectedVnet = "cluster-vnet"
				}
				if ctx != t.Context() || infraID != "infra" || clusterRG != "cluster-rg" || dnsRG != "DNS-RG" || nsgRG != expectedNSG || vnetRG != expectedVnet || customRoles {
					t.Fatalf("unexpected cleanup inputs: %s %s %s %s %s custom=%t", infraID, clusterRG, nsgRG, vnetRG, dnsRG, customRoles)
				}
				return test.cleanupErr
			})
			if !called || (test.notify && !errors.Is(notified, test.cleanupErr)) || (!test.notify && notified != nil) {
				t.Fatalf("cleanup called=%t, notification=%v, cleanup error=%v", called, notified, test.cleanupErr)
			}
		})
	}
}

func TestNewDestroyCommandClientProvider(t *testing.T) {
	t.Run("When management client creation fails, it should return the provider error", func(t *testing.T) {
		g := NewWithT(t)
		cmd := NewDestroyCommand(&core.DestroyOptions{}, &core.ClientProvider{
			ControllerRuntimeClient: func(string) (crclient.Client, error) {
				return nil, errors.New("management client unavailable")
			},
		})

		cmd.SetArgs([]string{"--azure-creds=creds", "--dns-zone-rg-name=zone"})
		err := cmd.Execute()
		g.Expect(err).To(MatchError("management client unavailable"))
	})
}

func TestDestroyClusterSetsCloudFromHostedCluster(t *testing.T) {
	tests := map[string]struct {
		hostedCluster *hyperv1.HostedCluster
		initialCloud  string
		expectedCloud string
		expectError   bool
	}{
		"When HostedCluster has a custom cloud, it should set Cloud to that value": {
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					InfraID: "test-infra",
					Platform: hyperv1.PlatformSpec{
						Azure: &hyperv1.AzurePlatformSpec{
							Location: "eastus",
							Cloud:    "AzureUSGovernmentCloud",
						},
					},
				},
			},
			expectedCloud: "AzureUSGovernmentCloud",
		},
		"When HostedCluster has empty cloud, it should default to DefaultAzureCloud": {
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					InfraID: "test-infra",
					Platform: hyperv1.PlatformSpec{
						Azure: &hyperv1.AzurePlatformSpec{
							Location: "eastus",
							Cloud:    "",
						},
					},
				},
			},
			expectedCloud: config.DefaultAzureCloud,
		},
		"When HostedCluster is nil and no cloud is set, it should default to DefaultAzureCloud": {
			hostedCluster: nil,
			expectedCloud: config.DefaultAzureCloud,
		},
		"When HostedCluster is nil and caller provided a cloud, it should preserve the caller value": {
			hostedCluster: nil,
			initialCloud:  "AzureUSGovernmentCloud",
			expectedCloud: "AzureUSGovernmentCloud",
		},
		"When HostedCluster has nil Azure platform, it should return an error": {
			hostedCluster: &hyperv1.HostedCluster{
				Spec: hyperv1.HostedClusterSpec{
					Platform: hyperv1.PlatformSpec{},
				},
			},
			expectError: true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			opts := &core.DestroyOptions{
				ClusterGracePeriod: 10 * time.Minute,
				Log:                log.Log,
				AzurePlatform: core.AzurePlatformDestroyOptions{
					CredentialsFile: "/fake/creds",
					Location:        "eastus",
					Cloud:           test.initialCloud,
				},
			}

			err := applyHostedClusterToDestroyOptions(opts, test.hostedCluster)

			if test.expectError {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(opts.AzurePlatform.Cloud).To(Equal(test.expectedCloud))
		})
	}
}

func TestDestroyClusterSetsGracePeriodFromTopology(t *testing.T) {
	tests := map[string]struct {
		topology            hyperv1.AzureTopologyType
		initialGracePeriod  time.Duration
		expectedGracePeriod time.Duration
		nilHC               bool
	}{
		"When topology is Private it should increase grace period to 20 minutes": {
			topology:            hyperv1.AzureTopologyPrivate,
			initialGracePeriod:  defaultClusterGracePeriod,
			expectedGracePeriod: privateClusterGracePeriod,
		},
		"When topology is PublicAndPrivate it should increase grace period to 20 minutes": {
			topology:            hyperv1.AzureTopologyPublicAndPrivate,
			initialGracePeriod:  defaultClusterGracePeriod,
			expectedGracePeriod: privateClusterGracePeriod,
		},
		"When topology is Public it should keep the default grace period": {
			topology:            hyperv1.AzureTopologyPublic,
			initialGracePeriod:  defaultClusterGracePeriod,
			expectedGracePeriod: defaultClusterGracePeriod,
		},
		"When topology is empty it should keep the default grace period": {
			topology:            "",
			initialGracePeriod:  defaultClusterGracePeriod,
			expectedGracePeriod: defaultClusterGracePeriod,
		},
		"When user explicitly set a custom grace period it should not override for Private topology": {
			topology:            hyperv1.AzureTopologyPrivate,
			initialGracePeriod:  30 * time.Minute,
			expectedGracePeriod: 30 * time.Minute,
		},
		"When user explicitly set a custom grace period it should not override for PublicAndPrivate topology": {
			topology:            hyperv1.AzureTopologyPublicAndPrivate,
			initialGracePeriod:  5 * time.Minute,
			expectedGracePeriod: 5 * time.Minute,
		},
		"When HostedCluster is nil it should keep the default grace period": {
			topology:            "",
			initialGracePeriod:  defaultClusterGracePeriod,
			expectedGracePeriod: defaultClusterGracePeriod,
			nilHC:               true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			opts := &core.DestroyOptions{
				ClusterGracePeriod: test.initialGracePeriod,
				Log:                log.Log,
				AzurePlatform: core.AzurePlatformDestroyOptions{
					CredentialsFile: "/fake/creds",
					Location:        "eastus",
				},
			}

			var hc *hyperv1.HostedCluster
			if !test.nilHC {
				hc = &hyperv1.HostedCluster{
					Spec: hyperv1.HostedClusterSpec{
						InfraID: "test-infra",
						Platform: hyperv1.PlatformSpec{
							Azure: &hyperv1.AzurePlatformSpec{
								Location: "eastus",
								Topology: test.topology,
							},
						},
					},
				}
			}

			err := applyHostedClusterToDestroyOptions(opts, hc)
			g.Expect(err).ToNot(HaveOccurred())

			g.Expect(opts.ClusterGracePeriod).To(Equal(test.expectedGracePeriod))
		})
	}
}

func TestIsResourceGroupNotFound(t *testing.T) {
	tests := map[string]struct {
		err      error
		expected bool
	}{
		"When err is an azcore.ResponseError with 404 status, it should return true": {
			err:      &azcore.ResponseError{StatusCode: http.StatusNotFound},
			expected: true,
		},
		"When err is an azcore.ResponseError with a non-404 status, it should return false": {
			err:      &azcore.ResponseError{StatusCode: http.StatusForbidden},
			expected: false,
		},
		"When err is a generic error, it should return false": {
			err:      fmt.Errorf("some unrelated failure"),
			expected: false,
		},
		"When err wraps an azcore.ResponseError with 404 status, it should return true": {
			err:      fmt.Errorf("wrapped: %w", &azcore.ResponseError{StatusCode: http.StatusNotFound}),
			expected: true,
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			g.Expect(isResourceGroupNotFound(test.err)).To(Equal(test.expected))
		})
	}
}

type fakeResourceGroupClient struct {
	err error
}

func (f fakeResourceGroupClient) Get(context.Context, string, *armresources.ResourceGroupsClientGetOptions) (armresources.ResourceGroupsClientGetResponse, error) {
	return armresources.ResourceGroupsClientGetResponse{}, f.err
}

func TestCheckResourceGroup(t *testing.T) {
	tests := map[string]struct {
		err         error
		expectError bool
	}{
		"When the resource group returns 404, it should continue": {
			err: &azcore.ResponseError{StatusCode: http.StatusNotFound},
		},
		"When the resource group returns a wrapped 404, it should continue": {
			err: fmt.Errorf("wrapped: %w", &azcore.ResponseError{StatusCode: http.StatusNotFound}),
		},
		"When the resource group returns a non-404 error, it should return an error": {
			err:         &azcore.ResponseError{StatusCode: http.StatusForbidden},
			expectError: true,
		},
		"When the resource group lookup succeeds, it should continue": {},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			err := checkResourceGroup(context.Background(), fakeResourceGroupClient{err: test.err}, "test-resource-group", log.Log)
			if test.expectError {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring("failed to get resource group name, 'test-resource-group'"))
				return
			}
			g.Expect(err).ToNot(HaveOccurred())
		})
	}
}
