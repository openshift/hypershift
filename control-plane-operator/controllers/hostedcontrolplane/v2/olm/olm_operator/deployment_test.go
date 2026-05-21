package olmoperator

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
		name                   string
		olmCatalogPlacement    hyperv1.OLMCatalogPlacement
		expectedNoProxyHosts   []string
		expectedReleaseVersion string
	}{
		{
			name:                   "When OLMCatalogPlacement is Management, it should set NO_PROXY with catalog services",
			olmCatalogPlacement:    hyperv1.ManagementOLMCatalogPlacement,
			expectedNoProxyHosts:   []string{"kube-apiserver", "certified-operators", "community-operators", "redhat-operators", "redhat-marketplace"},
			expectedReleaseVersion: "4.15.0",
		},
		{
			name:                   "When OLMCatalogPlacement is Guest, it should set NO_PROXY without catalog services",
			olmCatalogPlacement:    hyperv1.GuestOLMCatalogPlacement,
			expectedNoProxyHosts:   []string{"kube-apiserver"},
			expectedReleaseVersion: "4.15.0",
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
			olmOperatorContainer := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
			g.Expect(olmOperatorContainer).ToNot(BeNil())

			// Check RELEASE_VERSION
			g.Expect(olmOperatorContainer.Env).To(ContainElement(
				corev1.EnvVar{Name: "RELEASE_VERSION", Value: tc.expectedReleaseVersion},
			))

			// Check NO_PROXY contains expected hosts
			var noProxyEnv *corev1.EnvVar
			for i := range olmOperatorContainer.Env {
				if olmOperatorContainer.Env[i].Name == "NO_PROXY" {
					noProxyEnv = &olmOperatorContainer.Env[i]
					break
				}
			}
			g.Expect(noProxyEnv).ToNot(BeNil())
			actualNoProxyHosts := strings.Split(noProxyEnv.Value, ",")
			g.Expect(actualNoProxyHosts).To(ConsistOf(tc.expectedNoProxyHosts))
		})
	}
}
