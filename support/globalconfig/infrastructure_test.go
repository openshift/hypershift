package globalconfig

import (
	"fmt"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/openstack"

	configv1 "github.com/openshift/api/config/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

// assertOnlyExpectedPlatformStatus verifies that only the platform status matching
// the given platform type is set, and all others are nil.
func assertOnlyExpectedPlatformStatus(g Gomega, infra *configv1.Infrastructure, platform hyperv1.PlatformType) {
	type platformCheck struct {
		name   string
		field  any
		expect bool
	}
	checks := []platformCheck{
		{"AWS", infra.Status.PlatformStatus.AWS, platform == hyperv1.AWSPlatform},
		{"Azure", infra.Status.PlatformStatus.Azure, platform == hyperv1.AzurePlatform},
		{"GCP", infra.Status.PlatformStatus.GCP, platform == hyperv1.GCPPlatform},
		{"PowerVS", infra.Status.PlatformStatus.PowerVS, platform == hyperv1.PowerVSPlatform},
		{"OpenStack", infra.Status.PlatformStatus.OpenStack, platform == hyperv1.OpenStackPlatform},
	}
	for _, c := range checks {
		if c.expect {
			g.Expect(c.field).ToNot(BeNil(), "expected %s platform status to be set", c.name)
		} else {
			g.Expect(c.field).To(BeNil(), "expected %s platform status to be nil when platform is %s", c.name, platform)
		}
	}
}

func TestReconcileInfrastructure(t *testing.T) {
	const (
		fakeHCPName          = "test-cluster"
		fakeInfraID          = "test-infra-id"
		fakeBaseDomain       = "example.com"
		fakeAPIServerAddress = "api.example.com"
		fakeAPIServerPort    = int32(6443)
	)

	baseHCP := func(platform hyperv1.PlatformType) *hyperv1.HostedControlPlane {
		return &hyperv1.HostedControlPlane{
			ObjectMeta: metav1.ObjectMeta{
				Name: fakeHCPName,
			},
			Spec: hyperv1.HostedControlPlaneSpec{
				InfraID: fakeInfraID,
				DNS: hyperv1.DNSSpec{
					BaseDomain: fakeBaseDomain,
				},
				Platform: hyperv1.PlatformSpec{
					Type: platform,
				},
				InfrastructureAvailabilityPolicy: hyperv1.SingleReplica,
			},
			Status: hyperv1.HostedControlPlaneStatus{
				ControlPlaneEndpoint: hyperv1.APIEndpoint{
					Host: fakeAPIServerAddress,
					Port: fakeAPIServerPort,
				},
			},
		}
	}

	testsCases := []struct {
		name       string
		inputHCP   *hyperv1.HostedControlPlane
		inputInfra *configv1.Infrastructure
		verify     func(g Gomega, infra *configv1.Infrastructure)
	}{
		// Common fields
		{
			name:       "When a platform is specified, it should set platform type in spec and status",
			inputInfra: InfrastructureConfig(),
			inputHCP:   baseHCP(hyperv1.NonePlatform),
			verify: func(g Gomega, infra *configv1.Infrastructure) {
				g.Expect(infra.Spec.PlatformSpec.Type).To(Equal(configv1.NonePlatformType))
				g.Expect(infra.Status.Platform).To(Equal(configv1.NonePlatformType))
				g.Expect(infra.Status.PlatformStatus).ToNot(BeNil())
				g.Expect(infra.Status.PlatformStatus.Type).To(Equal(configv1.NonePlatformType))
			},
		},
		{
			name:       "When control plane endpoint is set, it should set API server URLs",
			inputInfra: InfrastructureConfig(),
			inputHCP:   baseHCP(hyperv1.NonePlatform),
			verify: func(g Gomega, infra *configv1.Infrastructure) {
				wantURL := fmt.Sprintf("https://%s:%d", fakeAPIServerAddress, fakeAPIServerPort)
				g.Expect(infra.Status.APIServerURL).To(Equal(wantURL))
				g.Expect(infra.Status.APIServerInternalURL).To(Equal(wantURL))
			},
		},
		{
			name:       "When HCP has DNS config, it should set EtcdDiscoveryDomain from BaseDomain",
			inputInfra: InfrastructureConfig(),
			inputHCP:   baseHCP(hyperv1.NonePlatform),
			verify: func(g Gomega, infra *configv1.Infrastructure) {
				g.Expect(infra.Status.EtcdDiscoveryDomain).To(Equal(fmt.Sprintf("%s.%s", fakeHCPName, fakeBaseDomain)))
			},
		},
		{
			name:       "When HCP has InfraID, it should set InfrastructureName",
			inputInfra: InfrastructureConfig(),
			inputHCP:   baseHCP(hyperv1.NonePlatform),
			verify: func(g Gomega, infra *configv1.Infrastructure) {
				g.Expect(infra.Status.InfrastructureName).To(Equal(fakeInfraID))
			},
		},
		{
			name:       "When any platform is specified, it should set ControlPlaneTopology to External",
			inputInfra: InfrastructureConfig(),
			inputHCP:   baseHCP(hyperv1.NonePlatform),
			verify: func(g Gomega, infra *configv1.Infrastructure) {
				g.Expect(infra.Status.ControlPlaneTopology).To(Equal(configv1.ExternalTopologyMode))
			},
		},
		{
			name:       "When SingleReplica availability policy is set, it should use SingleReplica topology",
			inputInfra: InfrastructureConfig(),
			inputHCP:   baseHCP(hyperv1.NonePlatform),
			verify: func(g Gomega, infra *configv1.Infrastructure) {
				g.Expect(infra.Status.InfrastructureTopology).To(Equal(configv1.SingleReplicaTopologyMode))
			},
		},
		{
			name:       "When HighlyAvailable availability policy is set, it should use HighlyAvailable topology",
			inputInfra: InfrastructureConfig(),
			inputHCP: func() *hyperv1.HostedControlPlane {
				hcp := baseHCP(hyperv1.NonePlatform)
				hcp.Spec.InfrastructureAvailabilityPolicy = hyperv1.HighlyAvailable
				return hcp
			}(),
			verify: func(g Gomega, infra *configv1.Infrastructure) {
				g.Expect(infra.Status.InfrastructureTopology).To(Equal(configv1.HighlyAvailableTopologyMode))
				g.Expect(infra.Status.ControlPlaneTopology).To(Equal(configv1.ExternalTopologyMode))
			},
		},
		{
			name:       "When AWS HCP is private, it should use hypershift.local for APIServerInternalURL",
			inputInfra: InfrastructureConfig(),
			inputHCP: func() *hyperv1.HostedControlPlane {
				hcp := baseHCP(hyperv1.AWSPlatform)
				hcp.Spec.Platform.AWS = &hyperv1.AWSPlatformSpec{
					Region:         "us-east-1",
					EndpointAccess: hyperv1.Private,
				}
				return hcp
			}(),
			verify: func(g Gomega, infra *configv1.Infrastructure) {
				want := fmt.Sprintf("https://api.%s.hypershift.local:%d", fakeHCPName, fakeAPIServerPort)
				g.Expect(infra.Status.APIServerInternalURL).To(Equal(want))
			},
		},
		{
			name:       "When KubeAPIServerDNSName is set, it should override APIServerURL",
			inputInfra: InfrastructureConfig(),
			inputHCP: func() *hyperv1.HostedControlPlane {
				hcp := baseHCP(hyperv1.NonePlatform)
				hcp.Spec.KubeAPIServerDNSName = "custom-api.example.com"
				return hcp
			}(),
			verify: func(g Gomega, infra *configv1.Infrastructure) {
				g.Expect(infra.Status.APIServerURL).To(Equal(fmt.Sprintf("https://custom-api.example.com:%d", fakeAPIServerPort)))
				g.Expect(infra.Status.APIServerInternalURL).To(Equal(fmt.Sprintf("https://%s:%d", fakeAPIServerAddress, fakeAPIServerPort)))
			},
		},

		// AWS platform
		{
			name:       "When AWS platform is specified, it should set region and initialize platform structs",
			inputInfra: InfrastructureConfig(),
			inputHCP: func() *hyperv1.HostedControlPlane {
				hcp := baseHCP(hyperv1.AWSPlatform)
				hcp.Spec.Platform.AWS = &hyperv1.AWSPlatformSpec{
					Region: "us-east-1",
				}
				return hcp
			}(),
			verify: func(g Gomega, infra *configv1.Infrastructure) {
				g.Expect(infra.Status.Platform).To(Equal(configv1.AWSPlatformType))
				g.Expect(infra.Spec.PlatformSpec.AWS).ToNot(BeNil())
				g.Expect(infra.Status.PlatformStatus.AWS).ToNot(BeNil())
				g.Expect(infra.Status.PlatformStatus.AWS.Region).To(Equal("us-east-1"))
			},
		},
		{
			name:       "When AWS platform has resource tags, it should copy tags filtering out kubernetes.io prefixed ones",
			inputInfra: InfrastructureConfig(),
			inputHCP: func() *hyperv1.HostedControlPlane {
				hcp := baseHCP(hyperv1.AWSPlatform)
				hcp.Spec.Platform.AWS = &hyperv1.AWSPlatformSpec{
					Region: "us-east-1",
					ResourceTags: []hyperv1.AWSResourceTag{
						{Key: "team", Value: "platform"},
						{Key: "kubernetes.io/cluster/test", Value: "owned"},
						{Key: "env", Value: "prod"},
					},
				}
				return hcp
			}(),
			verify: func(g Gomega, infra *configv1.Infrastructure) {
				g.Expect(infra.Status.PlatformStatus.AWS).ToNot(BeNil())
				g.Expect(infra.Status.PlatformStatus.AWS.ResourceTags).To(Equal([]configv1.AWSResourceTag{
					{Key: "team", Value: "platform"},
					{Key: "env", Value: "prod"},
				}))
			},
		},

		// Azure platform
		{
			name:       "When Azure platform is specified with a cloud name, it should set CloudName and ResourceGroupName",
			inputInfra: InfrastructureConfig(),
			inputHCP: func() *hyperv1.HostedControlPlane {
				hcp := baseHCP(hyperv1.AzurePlatform)
				hcp.Spec.Platform.Azure = &hyperv1.AzurePlatformSpec{
					Cloud:             "AzureUSGovernmentCloud",
					ResourceGroupName: "my-rg",
				}
				return hcp
			}(),
			verify: func(g Gomega, infra *configv1.Infrastructure) {
				g.Expect(infra.Status.Platform).To(Equal(configv1.AzurePlatformType))
				g.Expect(infra.Status.PlatformStatus.Azure).ToNot(BeNil())
				g.Expect(infra.Status.PlatformStatus.Azure.CloudName).To(Equal(configv1.AzureUSGovernmentCloud))
				g.Expect(infra.Status.PlatformStatus.Azure.ResourceGroupName).To(Equal("my-rg"))
			},
		},
		{
			name:       "When Azure platform has empty cloud name, it should default to AzurePublicCloud",
			inputInfra: InfrastructureConfig(),
			inputHCP: func() *hyperv1.HostedControlPlane {
				hcp := baseHCP(hyperv1.AzurePlatform)
				hcp.Spec.Platform.Azure = &hyperv1.AzurePlatformSpec{
					Cloud:             "",
					ResourceGroupName: "my-rg",
				}
				return hcp
			}(),
			verify: func(g Gomega, infra *configv1.Infrastructure) {
				g.Expect(infra.Status.PlatformStatus.Azure).ToNot(BeNil())
				g.Expect(infra.Status.PlatformStatus.Azure.CloudName).To(Equal(configv1.AzurePublicCloud))
			},
		},
		{
			name:       "When Azure platform is specified, it should set CloudConfig name",
			inputInfra: InfrastructureConfig(),
			inputHCP: func() *hyperv1.HostedControlPlane {
				hcp := baseHCP(hyperv1.AzurePlatform)
				hcp.Spec.Platform.Azure = &hyperv1.AzurePlatformSpec{
					ResourceGroupName: "my-rg",
				}
				return hcp
			}(),
			verify: func(g Gomega, infra *configv1.Infrastructure) {
				g.Expect(infra.Spec.CloudConfig.Name).To(Equal("cloud.conf"))
			},
		},

		// PowerVS platform
		{
			name:       "When PowerVS platform is specified, it should set Region Zone CISInstanceCRN and ResourceGroup",
			inputInfra: InfrastructureConfig(),
			inputHCP: func() *hyperv1.HostedControlPlane {
				hcp := baseHCP(hyperv1.PowerVSPlatform)
				hcp.Spec.Platform.PowerVS = &hyperv1.PowerVSPlatformSpec{
					Region:         "us-south",
					Zone:           "us-south-1",
					CISInstanceCRN: "crn:v1:bluemix:public:internet-svcs:global:a/abc123:::",
					ResourceGroup:  "my-resource-group",
				}
				return hcp
			}(),
			verify: func(g Gomega, infra *configv1.Infrastructure) {
				g.Expect(infra.Status.Platform).To(Equal(configv1.PowerVSPlatformType))
				g.Expect(infra.Status.PlatformStatus.PowerVS).ToNot(BeNil())
				g.Expect(infra.Status.PlatformStatus.PowerVS.Region).To(Equal("us-south"))
				g.Expect(infra.Status.PlatformStatus.PowerVS.Zone).To(Equal("us-south-1"))
				g.Expect(infra.Status.PlatformStatus.PowerVS.CISInstanceCRN).To(Equal("crn:v1:bluemix:public:internet-svcs:global:a/abc123:::"))
				g.Expect(infra.Status.PlatformStatus.PowerVS.ResourceGroup).To(Equal("my-resource-group"))
			},
		},

		// OpenStack platform
		{
			name:       "When OpenStack platform is specified, it should set platform spec and cloud config",
			inputInfra: InfrastructureConfig(),
			inputHCP:   baseHCP(hyperv1.OpenStackPlatform),
			verify: func(g Gomega, infra *configv1.Infrastructure) {
				g.Expect(infra.Status.Platform).To(Equal(configv1.OpenStackPlatformType))
				g.Expect(infra.Spec.PlatformSpec.OpenStack).ToNot(BeNil())
				g.Expect(infra.Spec.CloudConfig.Name).To(Equal("cloud-provider-config"))
				g.Expect(infra.Spec.CloudConfig.Key).To(Equal(openstack.CloudConfigKey))
			},
		},
		{
			name:       "When OpenStack platform is specified, it should set status with correct defaults",
			inputInfra: InfrastructureConfig(),
			inputHCP:   baseHCP(hyperv1.OpenStackPlatform),
			verify: func(g Gomega, infra *configv1.Infrastructure) {
				g.Expect(infra.Status.PlatformStatus.OpenStack).ToNot(BeNil())
				g.Expect(infra.Status.PlatformStatus.OpenStack.CloudName).To(Equal("openstack"))
				g.Expect(infra.Status.PlatformStatus.OpenStack.LoadBalancer).ToNot(BeNil())
				g.Expect(infra.Status.PlatformStatus.OpenStack.LoadBalancer.Type).To(Equal(configv1.LoadBalancerTypeUserManaged))
				g.Expect(infra.Status.PlatformStatus.OpenStack.APIServerInternalIPs).To(BeEmpty())
				g.Expect(infra.Status.PlatformStatus.OpenStack.IngressIPs).To(BeEmpty())
			},
		},

		// GCP platform
		{
			name:       "When GCP platform is specified, it should set ProjectID and Region from HCP spec",
			inputInfra: InfrastructureConfig(),
			inputHCP: func() *hyperv1.HostedControlPlane {
				hcp := baseHCP(hyperv1.GCPPlatform)
				hcp.Spec.Platform.GCP = &hyperv1.GCPPlatformSpec{
					Project: "my-gcp-project-123",
					Region:  "us-central1",
				}
				return hcp
			}(),
			verify: func(g Gomega, infra *configv1.Infrastructure) {
				g.Expect(infra.Status.Platform).To(Equal(configv1.GCPPlatformType))
				g.Expect(infra.Status.PlatformStatus.GCP).ToNot(BeNil())
				g.Expect(infra.Status.PlatformStatus.GCP.ProjectID).To(Equal("my-gcp-project-123"))
				g.Expect(infra.Status.PlatformStatus.GCP.Region).To(Equal("us-central1"))
			},
		},
		{
			name:       "When GCP platform has resource labels, it should copy labels filtering out kubernetes-io prefixed ones",
			inputInfra: InfrastructureConfig(),
			inputHCP: func() *hyperv1.HostedControlPlane {
				hcp := baseHCP(hyperv1.GCPPlatform)
				hcp.Spec.Platform.GCP = &hyperv1.GCPPlatformSpec{
					Project: "my-gcp-project-123",
					Region:  "us-central1",
					ResourceLabels: []hyperv1.GCPResourceLabel{
						{Key: "team", Value: ptr.To("platform")},
						{Key: "kubernetes-io-cluster", Value: ptr.To("owned")},
						{Key: "env", Value: ptr.To("prod")},
						{Key: "no-value-label"},
					},
				}
				return hcp
			}(),
			verify: func(g Gomega, infra *configv1.Infrastructure) {
				g.Expect(infra.Status.PlatformStatus.GCP).ToNot(BeNil())
				g.Expect(infra.Status.PlatformStatus.GCP.ResourceLabels).To(Equal([]configv1.GCPResourceLabel{
					{Key: "team", Value: "platform"},
					{Key: "env", Value: "prod"},
					{Key: "no-value-label", Value: ""},
				}))
			},
		},
		{
			name: "When GCP PlatformStatus is already initialized, it should update existing fields from HCP spec",
			inputInfra: func() *configv1.Infrastructure {
				infra := InfrastructureConfig()
				infra.Status.PlatformStatus = &configv1.PlatformStatus{
					GCP: &configv1.GCPPlatformStatus{
						ProjectID: "old-project",
						Region:    "old-region",
					},
				}
				return infra
			}(),
			inputHCP: func() *hyperv1.HostedControlPlane {
				hcp := baseHCP(hyperv1.GCPPlatform)
				hcp.Spec.Platform.GCP = &hyperv1.GCPPlatformSpec{
					Project: "new-project",
					Region:  "new-region",
				}
				return hcp
			}(),
			verify: func(g Gomega, infra *configv1.Infrastructure) {
				g.Expect(infra.Status.PlatformStatus.GCP).ToNot(BeNil())
				g.Expect(infra.Status.PlatformStatus.GCP.ProjectID).To(Equal("new-project"))
				g.Expect(infra.Status.PlatformStatus.GCP.Region).To(Equal("new-region"))
			},
		},
	}
	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			ReconcileInfrastructure(tc.inputInfra, tc.inputHCP)
			tc.verify(g, tc.inputInfra)
			assertOnlyExpectedPlatformStatus(g, tc.inputInfra, tc.inputHCP.Spec.Platform.Type)
		})
	}
}
