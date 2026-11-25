package registryoperator

import (
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

	// The two payloads deliberately differ: the shared fixtures wire the same fake
	// provider into both, so they cannot tell which release an image came from.
	controlPlaneRelease := testutil.FakeImageProvider(
		testutil.WithVersion("4.21.11"),
		testutil.WithImages(map[string]string{
			"cluster-image-registry-operator": "quay.io/openshift-release-dev/ocp-release-private@sha256:1111111111111111111111111111111111111111111111111111111111111111",
			"docker-registry":                 "quay.io/openshift-release-dev/ocp-release-private@sha256:2222222222222222222222222222222222222222222222222222222222222222",
			"cli":                             "quay.io/openshift-release-dev/ocp-release-private@sha256:3333333333333333333333333333333333333333333333333333333333333333",
		}),
	)
	userRelease := testutil.FakeImageProvider(
		testutil.WithVersion("4.21.10"),
		testutil.WithImages(map[string]string{
			"cluster-image-registry-operator": "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			"docker-registry":                 "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			"cli":                             "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		}),
	)

	testCases := []struct {
		name     string
		envVar   string
		expected string
	}{
		{
			// OPERATOR_IMAGE is only stamped into the node-ca DaemonSet and the Azure
			// path fix Job, both of which run on guest nodes.
			name:     "When the releases differ, it should set OPERATOR_IMAGE from the user release",
			envVar:   "OPERATOR_IMAGE",
			expected: userRelease.GetImage("cluster-image-registry-operator"),
		},
		{
			name:     "When the releases differ, it should set IMAGE from the user release",
			envVar:   "IMAGE",
			expected: userRelease.GetImage("docker-registry"),
		},
		{
			name:     "When the releases differ, it should set IMAGE_PRUNER from the user release",
			envVar:   "IMAGE_PRUNER",
			expected: userRelease.GetImage("cli"),
		},
		{
			name:     "When the releases differ, it should set RELEASE_VERSION from the user release",
			envVar:   "RELEASE_VERSION",
			expected: "4.21.10",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			cpContext := component.WorkloadContext{
				HCP: &hyperv1.HostedControlPlane{
					ObjectMeta: metav1.ObjectMeta{Name: "example", Namespace: "clusters-example"},
					Spec: hyperv1.HostedControlPlaneSpec{
						Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform},
					},
				},
				ReleaseImageProvider:     controlPlaneRelease,
				UserReleaseImageProvider: userRelease,
			}

			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(adaptDeployment(cpContext, deployment)).To(Succeed())

			container := podspec.FindContainer(ComponentName, deployment.Spec.Template.Spec.Containers)
			g.Expect(container).ToNot(BeNil())

			var env *corev1.EnvVar
			for i := range container.Env {
				if container.Env[i].Name == tc.envVar {
					env = &container.Env[i]
				}
			}
			g.Expect(env).ToNot(BeNil(), "expected %s to be set", tc.envVar)
			g.Expect(env.Value).To(Equal(tc.expected))
		})
	}
}
