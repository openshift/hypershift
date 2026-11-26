package catalogoperator

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

	// The control plane and user releases deliberately carry different images, so
	// the test fails if the guest-facing images are taken from the control plane
	// release, which may be private to the management cluster.
	const (
		controlPlaneOLMOperatorImage      = "quay.io/openshift-release-dev/ocp-release-private@sha256:1111111111111111111111111111111111111111111111111111111111111111"
		controlPlaneOperatorRegistryImage = "quay.io/openshift-release-dev/ocp-release-private@sha256:2222222222222222222222222222222222222222222222222222222222222222"
		userOLMOperatorImage              = "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		userOperatorRegistryImage         = "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)

	testCases := []struct {
		name                          string
		olmCatalogPlacement           hyperv1.OLMCatalogPlacement
		expectedNoProxyHosts          []string
		expectedOLMOperatorImage      string
		expectedOperatorRegistryImage string
		expectedReleaseVersion        string
	}{
		{
			name:                          "When OLMCatalogPlacement is Management, it should set NO_PROXY with catalog services and guest workload images from the user release",
			olmCatalogPlacement:           hyperv1.ManagementOLMCatalogPlacement,
			expectedNoProxyHosts:          []string{"kube-apiserver", "certified-operators", "community-operators", "redhat-operators"},
			expectedOLMOperatorImage:      userOLMOperatorImage,
			expectedOperatorRegistryImage: userOperatorRegistryImage,
			expectedReleaseVersion:        "4.15.0",
		},
		{
			name:                          "When OLMCatalogPlacement is Guest, it should set NO_PROXY without catalog services and guest workload images from the user release",
			olmCatalogPlacement:           hyperv1.GuestOLMCatalogPlacement,
			expectedNoProxyHosts:          []string{"kube-apiserver"},
			expectedOLMOperatorImage:      userOLMOperatorImage,
			expectedOperatorRegistryImage: userOperatorRegistryImage,
			expectedReleaseVersion:        "4.15.0",
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
					OLMCatalogPlacement: tc.olmCatalogPlacement,
				},
			}

			controlPlaneReleaseProvider := testutil.FakeImageProvider(
				testutil.WithVersion("4.15.1"),
				testutil.WithImages(map[string]string{
					"operator-lifecycle-manager": controlPlaneOLMOperatorImage,
					"operator-registry":          controlPlaneOperatorRegistryImage,
				}),
			)
			userReleaseProvider := testutil.FakeImageProvider(
				testutil.WithVersion(tc.expectedReleaseVersion),
				testutil.WithImages(map[string]string{
					"operator-lifecycle-manager": userOLMOperatorImage,
					"operator-registry":          userOperatorRegistryImage,
				}),
			)

			cpContext := component.WorkloadContext{
				HCP:                      hcp,
				ReleaseImageProvider:     controlPlaneReleaseProvider,
				UserReleaseImageProvider: userReleaseProvider,
			}

			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())

			err = adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			// Verify environment variables
			catalogOperatorContainer := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
			g.Expect(catalogOperatorContainer).ToNot(BeNil())

			// Check RELEASE_VERSION
			g.Expect(catalogOperatorContainer.Env).To(ContainElement(
				corev1.EnvVar{Name: "RELEASE_VERSION", Value: tc.expectedReleaseVersion},
			))

			// Check OLM_OPERATOR_IMAGE
			g.Expect(catalogOperatorContainer.Env).To(ContainElement(
				corev1.EnvVar{Name: "OLM_OPERATOR_IMAGE", Value: tc.expectedOLMOperatorImage},
			))

			// Check OPERATOR_REGISTRY_IMAGE
			g.Expect(catalogOperatorContainer.Env).To(ContainElement(
				corev1.EnvVar{Name: "OPERATOR_REGISTRY_IMAGE", Value: tc.expectedOperatorRegistryImage},
			))

			// Check NO_PROXY contains expected hosts
			var noProxyEnv *corev1.EnvVar
			for i := range catalogOperatorContainer.Env {
				if catalogOperatorContainer.Env[i].Name == "NO_PROXY" {
					noProxyEnv = &catalogOperatorContainer.Env[i]
					break
				}
			}
			g.Expect(noProxyEnv).ToNot(BeNil())
			actualNoProxyHosts := strings.Split(noProxyEnv.Value, ",")
			g.Expect(actualNoProxyHosts).To(ConsistOf(tc.expectedNoProxyHosts))
		})
	}
}
