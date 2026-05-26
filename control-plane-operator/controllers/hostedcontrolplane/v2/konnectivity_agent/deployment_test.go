package konnectivity

import (
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/infra"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	"github.com/openshift/hypershift/support/api"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"

	configv1 "github.com/openshift/api/config/v1"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestAdaptDeployment(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name                 string
		hcpAnnotations       map[string]string
		hcpConfiguration     *hyperv1.ClusterConfiguration
		infraStatus          infra.InfrastructureStatus
		expectedAgentIDCount int
		expectedImage        string
		validateAgentIDs     func(*testing.T, []string)
	}{
		{
			name:           "When OAuth is disabled it should add two agent identifiers",
			hcpAnnotations: map[string]string{},
			hcpConfiguration: &hyperv1.ClusterConfiguration{
				Authentication: &configv1.AuthenticationSpec{
					Type: configv1.AuthenticationTypeOIDC,
				},
			},
			infraStatus: infra.InfrastructureStatus{
				OpenShiftAPIHost:        "api.example.com",
				PackageServerAPIAddress: "pkgserver.example.com",
			},
			expectedAgentIDCount: 2,
			expectedImage:        "apiserver-network-proxy",
			validateAgentIDs: func(t *testing.T, ids []string) {
				g := NewWithT(t)
				g.Expect(ids).To(ContainElement("ipv4=api.example.com"))
				g.Expect(ids).To(ContainElement("ipv4=pkgserver.example.com"))
			},
		},
		{
			name:             "When OAuth is enabled it should add three agent identifiers",
			hcpAnnotations:   map[string]string{},
			hcpConfiguration: nil, // OAuth enabled by default when nil
			infraStatus: infra.InfrastructureStatus{
				OpenShiftAPIHost:        "api.example.com",
				PackageServerAPIAddress: "pkgserver.example.com",
				OauthAPIServerHost:      "oauth.example.com",
			},
			expectedAgentIDCount: 3,
			expectedImage:        "apiserver-network-proxy",
			validateAgentIDs: func(t *testing.T, ids []string) {
				g := NewWithT(t)
				g.Expect(ids).To(ContainElement("ipv4=api.example.com"))
				g.Expect(ids).To(ContainElement("ipv4=pkgserver.example.com"))
				g.Expect(ids).To(ContainElement("ipv4=oauth.example.com"))
			},
		},
		{
			name: "When custom konnectivity agent image is specified it should use that image",
			hcpAnnotations: map[string]string{
				hyperv1.KonnectivityAgentImageAnnotation: "custom-registry.io/konnectivity:v1.2.3",
			},
			hcpConfiguration: &hyperv1.ClusterConfiguration{
				Authentication: &configv1.AuthenticationSpec{
					Type: configv1.AuthenticationTypeOIDC,
				},
			},
			infraStatus: infra.InfrastructureStatus{
				OpenShiftAPIHost:        "api.example.com",
				PackageServerAPIAddress: "pkgserver.example.com",
			},
			expectedAgentIDCount: 2,
			expectedImage:        "custom-registry.io/konnectivity:v1.2.3",
			validateAgentIDs: func(t *testing.T, ids []string) {
				g := NewWithT(t)
				g.Expect(ids).To(ContainElement("ipv4=api.example.com"))
				g.Expect(ids).To(ContainElement("ipv4=pkgserver.example.com"))
			},
		},
		{
			name:             "When all IPs are different it should create unique agent identifiers",
			hcpAnnotations:   map[string]string{},
			hcpConfiguration: nil, // OAuth enabled by default
			infraStatus: infra.InfrastructureStatus{
				OpenShiftAPIHost:        "api.different.com",
				PackageServerAPIAddress: "pkgserver.different.com",
				OauthAPIServerHost:      "oauth.different.com",
			},
			expectedAgentIDCount: 3,
			expectedImage:        "apiserver-network-proxy",
			validateAgentIDs: func(t *testing.T, ids []string) {
				g := NewWithT(t)
				g.Expect(ids).To(ContainElement("ipv4=api.different.com"))
				g.Expect(ids).To(ContainElement("ipv4=pkgserver.different.com"))
				g.Expect(ids).To(ContainElement("ipv4=oauth.different.com"))
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:        "test-hcp",
					Namespace:   "test-namespace",
					Annotations: tc.hcpAnnotations,
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Configuration: tc.hcpConfiguration,
				},
				Status: hyperv1.HostedControlPlaneStatus{},
			}

			cpContext := component.WorkloadContext{
				Client:      fake.NewClientBuilder().WithScheme(api.Scheme).Build(),
				HCP:         hcp,
				InfraStatus: tc.infraStatus,
			}

			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())

			err = adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			// Verify container exists
			konnectivityContainer := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
			g.Expect(konnectivityContainer).ToNot(BeNil(), "konnectivity-agent container should exist")

			// Verify image
			g.Expect(konnectivityContainer.Image).To(Equal(tc.expectedImage))

			// Verify agent identifiers
			var agentIDsArg string
			for i, arg := range konnectivityContainer.Args {
				if arg == "--agent-identifiers" {
					g.Expect(i+1).To(BeNumerically("<", len(konnectivityContainer.Args)), "agent-identifiers should have a value")
					agentIDsArg = konnectivityContainer.Args[i+1]
					break
				}
			}
			g.Expect(agentIDsArg).ToNot(BeEmpty(), "--agent-identifiers argument should be present")

			// Parse agent identifiers
			agentIDs := strings.Split(agentIDsArg, "&")
			g.Expect(len(agentIDs)).To(Equal(tc.expectedAgentIDCount))

			tc.validateAgentIDs(t, agentIDs)
		})
	}
}
