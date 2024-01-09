package kubevirt

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"
	"k8s.io/utils/pointer"

	hyperv1 "github.com/openshift/hypershift/api/types/hypershift/v1beta1"
	"github.com/openshift/hypershift/cmd/cluster/core"
	apifixtures "github.com/openshift/hypershift/examples/fixtures"
)

func Test_ApplyPlatformSpecificValues(t *testing.T) {
	tests := map[string]struct {
		argsOpts             core.CreateOptions
		expectedPlatformOpts apifixtures.ExampleOptions
		expectedError        string
	}{
		"should succeed configuring additional networks": {
			argsOpts: core.CreateOptions{
				InfraID: "infra1",
				KubevirtPlatform: core.KubevirtPlatformCreateOptions{
					ServicePublishingStrategy: "Ingress",
					Cores:                     1,
					RootVolumeSize:            16,
					AdditionalNetworks: []string{
						"name:ns1/nad1",
						"name:ns2/nad2",
					},
				},
			},
			expectedPlatformOpts: apifixtures.ExampleOptions{
				InfraID: "infra1",
				Kubevirt: &apifixtures.ExampleKubevirtOptions{
					ServicePublishingStrategy: "Ingress",
					Cores:                     1,
					RootVolumeSize:            16,
					BaseDomainPassthrough:     true,
					AdditionalNetworks: []hyperv1.KubevirtNetwork{
						{
							Name: "ns1/nad1",
						},
						{
							Name: "ns2/nad2",
						},
					},
				},
			},
		},
		"should succeed configuring additional networks including default one explicitly": {
			argsOpts: core.CreateOptions{
				InfraID: "infra1",
				KubevirtPlatform: core.KubevirtPlatformCreateOptions{
					ServicePublishingStrategy: "Ingress",
					Cores:                     1,
					RootVolumeSize:            16,
					AdditionalNetworks: []string{
						"name:ns1/nad1",
						"name:ns2/nad2",
					},
					AttachDefaultNetwork: pointer.Bool(true),
				},
			},
			expectedPlatformOpts: apifixtures.ExampleOptions{
				InfraID: "infra1",
				Kubevirt: &apifixtures.ExampleKubevirtOptions{
					ServicePublishingStrategy: "Ingress",
					Cores:                     1,
					RootVolumeSize:            16,
					BaseDomainPassthrough:     true,
					AdditionalNetworks: []hyperv1.KubevirtNetwork{
						{
							Name: "ns1/nad1",
						},
						{
							Name: "ns2/nad2",
						},
					},
					AttachDefaultNetwork: pointer.Bool(true),
				},
			},
		},
		"should succeed configuring additional networks excluding default network": {
			argsOpts: core.CreateOptions{
				InfraID: "infra1",
				KubevirtPlatform: core.KubevirtPlatformCreateOptions{
					ServicePublishingStrategy: "Ingress",
					Cores:                     1,
					RootVolumeSize:            16,
					AdditionalNetworks: []string{
						"name:ns1/nad1",
						"name:ns2/nad2",
					},
					AttachDefaultNetwork: pointer.Bool(false),
				},
			},
			expectedPlatformOpts: apifixtures.ExampleOptions{
				InfraID: "infra1",
				Kubevirt: &apifixtures.ExampleKubevirtOptions{
					ServicePublishingStrategy: "Ingress",
					Cores:                     1,
					RootVolumeSize:            16,
					BaseDomainPassthrough:     true,
					AdditionalNetworks: []hyperv1.KubevirtNetwork{
						{
							Name: "ns1/nad1",
						},
						{
							Name: "ns2/nad2",
						},
					},
					AttachDefaultNetwork: pointer.Bool(false),
				},
			},
		},
		"should fail excluding default network without additional ones": {
			argsOpts: core.CreateOptions{
				InfraID: "infra1",
				KubevirtPlatform: core.KubevirtPlatformCreateOptions{
					ServicePublishingStrategy: "Ingress",
					Cores:                     1,
					RootVolumeSize:            16,
					AttachDefaultNetwork:      pointer.Bool(false),
				},
			},
			expectedError: "missing --additional-network. when --attach-default-network is false configuring an additional network is mandatory",
		},
		"should fail with unexpected additional network parameters": {
			argsOpts: core.CreateOptions{
				InfraID: "infra1",
				KubevirtPlatform: core.KubevirtPlatformCreateOptions{
					ServicePublishingStrategy: "Ingress",
					Cores:                     1,
					RootVolumeSize:            16,
					AdditionalNetworks: []string{
						"badfield:ns2/nad2",
					},
				},
			},
			expectedError: `failed to parse "--additional-network" flag: unknown param(s): badfield:ns2/nad2`,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			obtainedPlatformOpts := apifixtures.ExampleOptions{}
			err := ApplyPlatformSpecificsValues(context.TODO(), &obtainedPlatformOpts, &test.argsOpts)
			if test.expectedError != "" {
				g.Expect(err).To(MatchError(test.expectedError))
			} else {
				g.Expect(err).To(BeNil())
				g.Expect(obtainedPlatformOpts).To(Equal(test.expectedPlatformOpts))
			}
		})
	}
}
