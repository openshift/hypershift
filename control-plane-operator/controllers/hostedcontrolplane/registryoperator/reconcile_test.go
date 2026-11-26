package registryoperator

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/metrics"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestReconcileDeployment(t *testing.T) {
	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test",
			Namespace: "test-namespace",
		},
		Spec: hyperv1.HostedControlPlaneSpec{
			ReleaseImage: "quay.io/ocp-dev/test-release-image:latest",
			Platform: hyperv1.PlatformSpec{
				Type: hyperv1.AWSPlatform,
			},
			IssuerURL: "https://www.example.com",
		},
	}
	images := map[string]string{
		"cluster-image-registry-operator": "quay.io/openshift/cluster-image-registry-operator:latest",
		"token-minter":                    "quay.io/openshift/token-minter:latest",
		"docker-registry":                 "quay.io/openshift/docker-registry:latest",
		"cli":                             "quay.io/openshift/cli:latest",
	}
	deployment := manifests.ImageRegistryOperatorDeployment("test-namespace")
	imageProvider := imageprovider.NewFromImages(images)
	params := NewParams(hcp, "1.0.0", imageProvider, imageProvider, true)
	if err := ReconcileDeployment(deployment, params); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	deploymentYaml, err := util.SerializeResource(deployment, api.Scheme)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.CompareWithFixture(t, deploymentYaml)
}

func TestReconcileDeploymentImageSources(t *testing.T) {
	t.Parallel()

	// The two payloads deliberately differ: TestReconcileDeployment wires the same
	// provider into both, so it cannot tell which release an image came from.
	controlPlaneRelease := imageprovider.NewFromImages(map[string]string{
		"cluster-image-registry-operator": "quay.io/openshift-release-dev/ocp-release-private@sha256:1111111111111111111111111111111111111111111111111111111111111111",
		"token-minter":                    "quay.io/openshift-release-dev/ocp-release-private@sha256:2222222222222222222222222222222222222222222222222222222222222222",
		"docker-registry":                 "quay.io/openshift-release-dev/ocp-release-private@sha256:3333333333333333333333333333333333333333333333333333333333333333",
		"cli":                             "quay.io/openshift-release-dev/ocp-release-private@sha256:4444444444444444444444444444444444444444444444444444444444444444",
	})
	userRelease := imageprovider.NewFromImages(map[string]string{
		"cluster-image-registry-operator": "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		"token-minter":                    "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		"docker-registry":                 "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		"cli":                             "quay.io/openshift-release-dev/ocp-v4.0-art-dev@sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
	})

	hcp := &hyperv1.HostedControlPlane{
		ObjectMeta: metav1.ObjectMeta{Name: "example", Namespace: "clusters-example"},
		Spec: hyperv1.HostedControlPlaneSpec{
			Platform: hyperv1.PlatformSpec{Type: hyperv1.AWSPlatform},
		},
	}
	deployment := manifests.ImageRegistryOperatorDeployment(hcp.Namespace)
	params := NewParams(hcp, "4.18.10", controlPlaneRelease, userRelease, true)
	NewWithT(t).Expect(ReconcileDeployment(deployment, params)).To(Succeed())

	container := util.FindContainer(containerMain().Name, deployment.Spec.Template.Spec.Containers)
	NewWithT(t).Expect(container).ToNot(BeNil())

	t.Run("When the releases differ, it should run the operator from the control plane release", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		g.Expect(container.Image).To(Equal(controlPlaneRelease.GetImage("cluster-image-registry-operator")))
	})

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
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

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

func TestReconcilePodMonitor(t *testing.T) {
	pm := manifests.ImageRegistryOperatorPodMonitor("test-namespace")
	ReconcilePodMonitor(pm, "the-cluster-id", metrics.MetricsSetTelemetry)
	pmYaml, err := util.SerializeResource(pm, api.Scheme)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	testutil.CompareWithFixture(t, pmYaml)
}
