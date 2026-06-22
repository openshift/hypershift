package packageserver

import (
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"
	"github.com/openshift/hypershift/support/testutil"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAdaptDeployment(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name                         string
		platformType                 hyperv1.PlatformType
		olmCatalogPlacement          hyperv1.OLMCatalogPlacement
		controllerAvailabilityPolicy hyperv1.AvailabilityPolicy
		expectedNoProxyHosts         []string
		expectedReleaseVersion       string
		expectedReplicas             *int32
		expectedKASReadinessCheck    bool
	}{
		{
			name:                         "When OLMCatalogPlacement is Management, it should set NO_PROXY with catalog services",
			platformType:                 hyperv1.AWSPlatform,
			olmCatalogPlacement:          hyperv1.ManagementOLMCatalogPlacement,
			controllerAvailabilityPolicy: hyperv1.SingleReplica,
			expectedNoProxyHosts:         []string{"kube-apiserver", "certified-operators", "community-operators", "redhat-operators", "redhat-marketplace"},
			expectedReleaseVersion:       "4.15.0",
			expectedReplicas:             nil,
			expectedKASReadinessCheck:    true,
		},
		{
			name:                         "When OLMCatalogPlacement is Guest, it should set NO_PROXY without catalog services",
			platformType:                 hyperv1.AWSPlatform,
			olmCatalogPlacement:          hyperv1.GuestOLMCatalogPlacement,
			controllerAvailabilityPolicy: hyperv1.SingleReplica,
			expectedNoProxyHosts:         []string{"kube-apiserver"},
			expectedReleaseVersion:       "4.15.0",
			expectedReplicas:             nil,
			expectedKASReadinessCheck:    true,
		},
		{
			name:                         "When platform is IBMCloud with HighlyAvailable, it should set replicas to 2",
			platformType:                 hyperv1.IBMCloudPlatform,
			olmCatalogPlacement:          hyperv1.GuestOLMCatalogPlacement,
			controllerAvailabilityPolicy: hyperv1.HighlyAvailable,
			expectedNoProxyHosts:         []string{"kube-apiserver"},
			expectedReleaseVersion:       "4.15.0",
			expectedReplicas:             func() *int32 { r := int32(2); return &r }(),
			expectedKASReadinessCheck:    true,
		},
		{
			name:                         "When platform is IBMCloud with SingleReplica, it should not override replicas",
			platformType:                 hyperv1.IBMCloudPlatform,
			olmCatalogPlacement:          hyperv1.GuestOLMCatalogPlacement,
			controllerAvailabilityPolicy: hyperv1.SingleReplica,
			expectedNoProxyHosts:         []string{"kube-apiserver"},
			expectedReleaseVersion:       "4.15.0",
			expectedReplicas:             nil,
			expectedKASReadinessCheck:    true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			hcp := &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-hcp",
					Namespace: "test-namespace",
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: tc.platformType,
					},
					OLMCatalogPlacement:          tc.olmCatalogPlacement,
					ControllerAvailabilityPolicy: tc.controllerAvailabilityPolicy,
				},
			}

			releaseProvider := testutil.FakeImageProvider(testutil.WithVersion(tc.expectedReleaseVersion))

			cpContext := component.WorkloadContext{
				HCP:                      hcp,
				UserReleaseImageProvider: releaseProvider,
			}

			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())

			err = adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			// Verify environment variables
			packageServerContainer := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
			g.Expect(packageServerContainer).ToNot(BeNil())

			// Check RELEASE_VERSION
			g.Expect(packageServerContainer.Env).To(ContainElement(
				corev1.EnvVar{Name: "RELEASE_VERSION", Value: tc.expectedReleaseVersion},
			))

			// Check NO_PROXY contains expected hosts
			var noProxyEnv *corev1.EnvVar
			for i := range packageServerContainer.Env {
				if packageServerContainer.Env[i].Name == "NO_PROXY" {
					noProxyEnv = &packageServerContainer.Env[i]
					break
				}
			}
			g.Expect(noProxyEnv).ToNot(BeNil())
			actualNoProxyHosts := strings.Split(noProxyEnv.Value, ",")
			g.Expect(actualNoProxyHosts).To(ConsistOf(tc.expectedNoProxyHosts))

			// Verify replicas
			if tc.expectedReplicas != nil {
				g.Expect(deployment.Spec.Replicas).ToNot(BeNil())
				g.Expect(*deployment.Spec.Replicas).To(Equal(*tc.expectedReplicas))
			} else {
				// Verify replicas were not overridden from the manifest default
				originalDeployment, loadErr := assets.LoadDeploymentManifest(ComponentName)
				g.Expect(loadErr).ToNot(HaveOccurred())
				if originalDeployment.Spec.Replicas != nil {
					g.Expect(deployment.Spec.Replicas).ToNot(BeNil())
					g.Expect(*deployment.Spec.Replicas).To(Equal(*originalDeployment.Spec.Replicas))
				} else {
					g.Expect(deployment.Spec.Replicas).To(BeNil())
				}
			}

			// Verify KAS readiness check container is added
			if tc.expectedKASReadinessCheck {
				kasReadinessContainer := podspec.FindContainer("kas-readiness-check", deployment.Spec.Template.Spec.Containers)
				g.Expect(kasReadinessContainer).ToNot(BeNil(), "KAS readiness check container should be present")
			}
		})
	}
}
