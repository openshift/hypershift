package nto

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"
	"github.com/openshift/hypershift/support/testutil"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAdaptDeployment(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name                        string
		releaseVersion              string
		expectedReleaseVersionEnv   string
		expectedClusterNodeTunedEnv string
	}{
		{
			name:                        "When deployment is adapted with release info, it should set RELEASE_VERSION and CLUSTER_NODE_TUNED_IMAGE env vars",
			releaseVersion:              "4.15.0",
			expectedReleaseVersionEnv:   "4.15.0",
			expectedClusterNodeTunedEnv: "test-registry/cluster-node-tuning-operator:4.15.0",
		},
		{
			name:                        "When deployment is adapted with different release version, it should update env vars accordingly",
			releaseVersion:              "4.16.1",
			expectedReleaseVersionEnv:   "4.16.1",
			expectedClusterNodeTunedEnv: "test-registry/cluster-node-tuning-operator:4.16.1",
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
			}

			releaseProvider := testutil.FakeImageProvider(
				testutil.WithVersion(tc.releaseVersion),
				testutil.WithImages(map[string]string{
					ComponentName: "test-registry/" + ComponentName + ":" + tc.releaseVersion,
				}),
			)

			cpContext := component.WorkloadContext{
				Context:                  t.Context(),
				HCP:                      hcp,
				UserReleaseImageProvider: releaseProvider,
			}

			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())

			err = adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			// Verify container has the expected environment variables
			g.Expect(deployment.Spec.Template.Spec.Containers).ToNot(BeEmpty())
			container := deployment.Spec.Template.Spec.Containers[0]

			releaseVersionEnv := podspec.FindEnvVar("RELEASE_VERSION", container.Env)
			g.Expect(releaseVersionEnv).ToNot(BeNil())
			g.Expect(releaseVersionEnv.Value).To(Equal(tc.expectedReleaseVersionEnv))

			clusterNodeTunedImageEnv := podspec.FindEnvVar("CLUSTER_NODE_TUNED_IMAGE", container.Env)
			g.Expect(clusterNodeTunedImageEnv).ToNot(BeNil())
			g.Expect(clusterNodeTunedImageEnv.Value).To(Equal(tc.expectedClusterNodeTunedEnv))
		})
	}
}

func TestAdaptDeploymentUpdatesExistingEnvVars(t *testing.T) {
	t.Parallel()

	g := NewWithT(t)

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-hcp",
			Namespace: "test-namespace",
		},
	}

	releaseProvider := testutil.FakeImageProvider(
		testutil.WithVersion("4.15.0"),
		testutil.WithImages(map[string]string{
			ComponentName: "new-registry/cluster-node-tuning-operator:4.15.0",
		}),
	)

	cpContext := component.WorkloadContext{
		Context:                  t.Context(),
		HCP:                      hcp,
		UserReleaseImageProvider: releaseProvider,
	}

	deployment, err := assets.LoadDeploymentManifest(ComponentName)
	g.Expect(err).ToNot(HaveOccurred())

	// The deployment manifest already has these env vars with empty values,
	// so adaptDeployment should update them
	err = adaptDeployment(cpContext, deployment)
	g.Expect(err).ToNot(HaveOccurred())

	g.Expect(deployment.Spec.Template.Spec.Containers).ToNot(BeEmpty(), "deployment must have at least one container")
	container := deployment.Spec.Template.Spec.Containers[0]

	// Verify values were updated, not duplicated
	releaseVersionCount := 0
	clusterNodeTunedImageCount := 0
	for _, env := range container.Env {
		if env.Name == "RELEASE_VERSION" {
			releaseVersionCount++
			g.Expect(env.Value).To(Equal("4.15.0"))
		}
		if env.Name == "CLUSTER_NODE_TUNED_IMAGE" {
			clusterNodeTunedImageCount++
			g.Expect(env.Value).To(Equal("new-registry/cluster-node-tuning-operator:4.15.0"))
		}
	}

	g.Expect(releaseVersionCount).To(Equal(1), "RELEASE_VERSION should appear exactly once")
	g.Expect(clusterNodeTunedImageCount).To(Equal(1), "CLUSTER_NODE_TUNED_IMAGE should appear exactly once")
}
