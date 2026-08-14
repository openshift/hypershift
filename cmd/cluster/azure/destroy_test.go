package azure

import (
	"testing"
	"time"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	"github.com/openshift/hypershift/cmd/log"
	"github.com/openshift/hypershift/support/config"
)

func TestDestroyClusterSetsCloudFromHostedCluster(t *testing.T) {
	tests := map[string]struct {
		hostedCluster *hyperv1.HostedCluster
		initialCloud  string
		expectedCloud string
		expectError   bool
	}{
		"When HostedCluster has a custom cloud it should set Cloud to that value": {
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
		"When HostedCluster has empty cloud it should default to DefaultAzureCloud": {
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
		"When HostedCluster is nil and no cloud is set it should default to DefaultAzureCloud": {
			hostedCluster: nil,
			expectedCloud: config.DefaultAzureCloud,
		},
		"When HostedCluster is nil and caller provided a cloud it should preserve the caller value": {
			hostedCluster: nil,
			initialCloud:  "AzureUSGovernmentCloud",
			expectedCloud: "AzureUSGovernmentCloud",
		},
		"When HostedCluster has nil Azure platform it should return an error": {
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
