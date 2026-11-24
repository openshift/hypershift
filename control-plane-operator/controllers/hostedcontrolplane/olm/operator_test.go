package olm

import (
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestReconcileCatalogOperatorDeployment(t *testing.T) {
	tcs := []struct {
		name        string
		coResources *corev1.ResourceRequirements
	}{
		{
			name: "Preserve existing resources",
			coResources: &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("500m"),
					corev1.ResourceMemory: resource.MustParse("500Mi"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("1000m"),
					corev1.ResourceMemory: resource.MustParse("1000Mi"),
				},
			},
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			dep := &appsv1.Deployment{}
			if tc.coResources != nil {
				dep.Spec.Template.Spec.Containers = []corev1.Container{
					{
						Name:      catalogOperatorName,
						Resources: *tc.coResources,
					},
				}
			}

			if err := ReconcileCatalogOperatorDeployment(dep, config.OwnerRef{}, "", "", "", "", "", config.DeploymentConfig{}, "", []string{}, hyperv1.NonePlatform); err != nil {
				t.Fatalf("ReconcileCatalogOperatorDeployment: %v", err)
			}

			deploymentYaml, err := util.SerializeResource(dep, hyperapi.Scheme)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			testutil.CompareWithFixture(t, deploymentYaml)
		})
	}
}

func TestReconcileCatalogOperatorDeploymentImageSources(t *testing.T) {
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
	controlPlaneRelease := imageprovider.NewFromImages(map[string]string{
		"operator-lifecycle-manager": controlPlaneOLMOperatorImage,
		"operator-registry":          controlPlaneOperatorRegistryImage,
	})
	userRelease := imageprovider.NewFromImages(map[string]string{
		"operator-lifecycle-manager": userOLMOperatorImage,
		"operator-registry":          userOperatorRegistryImage,
	})

	hcp := &hyperv1.HostedControlPlane{}
	hcp.Name = "example"
	hcp.Namespace = "clusters-example"
	p := NewOperatorLifecycleManagerParams(hcp, controlPlaneRelease, userRelease, "4.18.10", true)

	deployment := &appsv1.Deployment{}
	NewWithT(t).Expect(ReconcileCatalogOperatorDeployment(deployment, p.OwnerRef, p.OLMImage, p.GuestOLMImage, p.ProxyImage, p.OperatorRegistryImage, p.ReleaseVersion, p.DeploymentConfig, p.AvailabilityProberImage, p.NoProxy, hyperv1.AWSPlatform)).To(Succeed())

	container := util.FindContainer(catalogOperatorName, deployment.Spec.Template.Spec.Containers)
	NewWithT(t).Expect(container).ToNot(BeNil())

	t.Run("When the releases differ, it should run catalog-operator from the control plane release", func(t *testing.T) {
		t.Parallel()
		g := NewWithT(t)
		g.Expect(container.Image).To(Equal(controlPlaneOLMOperatorImage))
	})

	testCases := []struct {
		name     string
		envVar   string
		expected string
	}{
		{
			name:     "When the releases differ, it should set OLM_OPERATOR_IMAGE from the user release",
			envVar:   "OLM_OPERATOR_IMAGE",
			expected: userOLMOperatorImage,
		},
		{
			name:     "When the releases differ, it should set OPERATOR_REGISTRY_IMAGE from the user release",
			envVar:   "OPERATOR_REGISTRY_IMAGE",
			expected: userOperatorRegistryImage,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)
			g.Expect(container.Env).To(ContainElement(corev1.EnvVar{Name: tc.envVar, Value: tc.expected}))
		})
	}
}

func TestReconcileOLMOperatorDeployment(t *testing.T) {
	tcs := []struct {
		name           string
		olmOpResources *corev1.ResourceRequirements
	}{
		{
			name: "Preserve existing resources",
			olmOpResources: &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("500m"),
					corev1.ResourceMemory: resource.MustParse("500Mi"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("1000m"),
					corev1.ResourceMemory: resource.MustParse("1000Mi"),
				},
			},
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			dep := &appsv1.Deployment{}
			if tc.olmOpResources != nil {
				dep.Spec.Template.Spec.Containers = []corev1.Container{
					{
						Name:      olmOperatorName,
						Resources: *tc.olmOpResources,
					},
				}
			}

			if err := ReconcileOLMOperatorDeployment(dep, config.OwnerRef{}, "", "", "", config.DeploymentConfig{}, "", []string{}, hyperv1.NonePlatform); err != nil {
				t.Fatalf("ReconcileOLMOperatorDeployment: %v", err)
			}

			deploymentYaml, err := util.SerializeResource(dep, hyperapi.Scheme)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			testutil.CompareWithFixture(t, deploymentYaml)
		})
	}
}
