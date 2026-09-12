package hostedclustersizing

import (
	"testing"

	. "github.com/onsi/gomega"

	schedulingv1alpha1 "github.com/openshift/hypershift/api/scheduling/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestResourceRequestsForSize(t *testing.T) {
	testCases := []struct {
		name     string
		size     string
		expected []schedulingv1alpha1.ResourceRequest
	}{
		{
			name: "When the size is small, it should return the baseline requests matching the CPO asset manifests",
			size: SizeSmall,
			expected: []schedulingv1alpha1.ResourceRequest{
				{DeploymentName: "kube-apiserver", ContainerName: "kube-apiserver", CPU: quantity("500m"), Memory: quantity("3Gi")},
				{DeploymentName: "etcd", ContainerName: "etcd", CPU: quantity("300m"), Memory: quantity("1Gi")},
				{DeploymentName: "openshift-apiserver", ContainerName: "openshift-apiserver", CPU: quantity("250m"), Memory: quantity("500Mi")},
			},
		},
		{
			name: "When the size is medium, it should return the scaled-up baseline requests",
			size: SizeMedium,
			expected: []schedulingv1alpha1.ResourceRequest{
				{DeploymentName: "kube-apiserver", ContainerName: "kube-apiserver", CPU: quantity("2"), Memory: quantity("8Gi")},
				{DeploymentName: "etcd", ContainerName: "etcd", CPU: quantity("1"), Memory: quantity("4Gi")},
				{DeploymentName: "openshift-apiserver", ContainerName: "openshift-apiserver", CPU: quantity("500m"), Memory: quantity("1Gi")},
			},
		},
		{
			name: "When the size is large, it should return the largest baseline requests",
			size: SizeLarge,
			expected: []schedulingv1alpha1.ResourceRequest{
				{DeploymentName: "kube-apiserver", ContainerName: "kube-apiserver", CPU: quantity("4"), Memory: quantity("16Gi")},
				{DeploymentName: "etcd", ContainerName: "etcd", CPU: quantity("2"), Memory: quantity("8Gi")},
				{DeploymentName: "openshift-apiserver", ContainerName: "openshift-apiserver", CPU: quantity("1"), Memory: quantity("2Gi")},
			},
		},
		{
			name:     "When the size is not a known t-shirt size, it should return no requests",
			size:     "extra-large",
			expected: []schedulingv1alpha1.ResourceRequest{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)
			g.Expect(resourceRequestsForSize(tc.size)).To(Equal(tc.expected))
		})
	}
}

func TestDefaultSizingConfig(t *testing.T) {
	t.Run("When the default sizing config is built, it should cover [0,+inf) and carry baseline resource requests for every size", func(t *testing.T) {
		g := NewWithT(t)

		config := DefaultSizingConfig()
		g.Expect(config.Name).To(Equal("cluster"))
		g.Expect(config.Spec.Sizes).To(HaveLen(3))

		var next uint32
		for i, size := range config.Spec.Sizes {
			g.Expect(size.Criteria.From).To(Equal(next), "size %q must start where the previous size ended", size.Name)
			if i == len(config.Spec.Sizes)-1 {
				g.Expect(size.Criteria.To).To(BeNil(), "the last size must have no upper limit")
			} else {
				g.Expect(size.Criteria.To).ToNot(BeNil(), "size %q must have an upper limit", size.Name)
				next = *size.Criteria.To + 1
			}

			g.Expect(size.Effects).ToNot(BeNil(), "size %q must declare effects", size.Name)
			g.Expect(size.Effects.ResourceRequests).To(Equal(resourceRequestsForSize(size.Name)))
		}
	})
}

// TestBaselineResourceRequestsMatchAssets enforces that the "small" baseline in
// baselineResourceRequests stays in lockstep with the requests hardcoded in the
// CPO asset manifests, so that a small HostedCluster is sized identically whether
// or not size tagging is enabled on the management cluster.
func TestBaselineResourceRequestsMatchAssets(t *testing.T) {
	assetRequests := map[string]corev1.ResourceList{}

	for _, componentName := range []string{"kube-apiserver", "openshift-apiserver"} {
		deployment, err := assets.LoadDeploymentManifest(componentName)
		if err != nil {
			t.Fatalf("failed to load %s deployment manifest: %v", componentName, err)
		}
		for _, container := range deployment.Spec.Template.Spec.Containers {
			assetRequests[componentName+"."+container.Name] = container.Resources.Requests
		}
	}

	// The etcd statefulset asset is a Go template; the values below only affect
	// object/label names, not the resource requests under test.
	statefulSet, err := assets.LoadStatefulSetManifestTemplated("etcd", map[string]string{
		"Name":                 "etcd",
		"DiscoveryServiceName": "etcd-discovery",
	})
	if err != nil {
		t.Fatalf("failed to load etcd statefulset manifest: %v", err)
	}
	for _, container := range statefulSet.Spec.Template.Spec.Containers {
		assetRequests["etcd."+container.Name] = container.Resources.Requests
	}

	for _, baseline := range baselineResourceRequests[SizeSmall] {
		key := baseline.deployment + "." + baseline.container
		t.Run("When the small baseline for "+key+" is compared to the asset manifest, it should match", func(t *testing.T) {
			g := NewWithT(t)

			requests, ok := assetRequests[key]
			g.Expect(ok).To(BeTrue(), "container %q not found in the asset manifests", key)
			g.Expect(requests.Cpu().String()).To(Equal(quantity(baseline.cpu).String()))
			g.Expect(requests.Memory().String()).To(Equal(quantity(baseline.memory).String()))
		})
	}
}

func quantity(value string) *resource.Quantity {
	parsed := resource.MustParse(value)
	return &parsed
}
