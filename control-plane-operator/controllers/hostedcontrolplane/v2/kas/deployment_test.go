package kas

import (
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	assets "github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/v2/assets"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/testutil"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestAddImagePrePullInitContainers(t *testing.T) {
	testCases := []struct {
		name                   string
		containers             []corev1.Container
		existingInitContainers []corev1.Container
		// expectedPrePullCount = unique container images - images already in init containers
		expectedPrePullCount int
	}{
		{
			name: "When containers have unique images it should create one pre-pull init container per image",
			containers: []corev1.Container{
				{Name: "kube-apiserver", Image: "registry.io/kube-apiserver:v1"},
				{Name: "konnectivity-server", Image: "registry.io/konnectivity:v1"},
			},
			existingInitContainers: []corev1.Container{
				{Name: "init-bootstrap-render", Image: "registry.io/cli:v1"},
			},
			expectedPrePullCount: 2,
		},
		{
			name: "When containers share the same image it should create only one pre-pull init container",
			containers: []corev1.Container{
				{Name: "container-a", Image: "registry.io/shared:v1"},
				{Name: "container-b", Image: "registry.io/shared:v1"},
			},
			existingInitContainers: []corev1.Container{
				{Name: "existing-init", Image: "registry.io/init:v1"},
			},
			expectedPrePullCount: 1,
		},
		{
			name: "When there are no existing init containers it should only have pre-pull init containers",
			containers: []corev1.Container{
				{Name: "kube-apiserver", Image: "registry.io/kube-apiserver:v1"},
			},
			existingInitContainers: []corev1.Container{},
			expectedPrePullCount:   1,
		},
		{
			// edge case that should never happen, still testing for robustness
			name:                   "When there are no existing containers it should have no pre-pull init containers",
			containers:             []corev1.Container{},
			existingInitContainers: []corev1.Container{},
			expectedPrePullCount:   0,
		},
		{
			name: "When a regular container uses the same image as an init container it should not pre-pull that image",
			containers: []corev1.Container{
				{Name: "kube-apiserver", Image: "registry.io/kube-apiserver:v1"},
				{Name: "sidecar", Image: "registry.io/cli:v1"}, // same image as init container
			},
			existingInitContainers: []corev1.Container{
				{Name: "init-bootstrap-render", Image: "registry.io/cli:v1"},
			},
			expectedPrePullCount: 1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			podSpec := &corev1.PodSpec{
				Containers:     tc.containers,
				InitContainers: tc.existingInitContainers,
			}

			addImagePrePullInitContainers(podSpec)

			// Collect unique images from regular containers
			regularContainerImages := make(map[string]bool)
			for _, container := range tc.containers {
				if container.Image != "" {
					regularContainerImages[container.Image] = true
				}
			}

			// Find pre-pull init containers and their positions
			prePullInitContainers := []corev1.Container{}
			firstOtherInitContainerIndex := -1
			lastPrePullInitContainerIndex := -1

			for i, initContainer := range podSpec.InitContainers {
				if strings.HasPrefix(initContainer.Name, "pre-pull-image-") {
					prePullInitContainers = append(prePullInitContainers, initContainer)
					lastPrePullInitContainerIndex = i
				} else {
					if firstOtherInitContainerIndex == -1 {
						firstOtherInitContainerIndex = i
					}
				}
			}

			// Validate the expected number of pre-pull init containers
			g.Expect(len(prePullInitContainers)).To(Equal(tc.expectedPrePullCount),
				"unexpected number of pre-pull init containers")

			// Validate that pre-pull init containers use images from regular containers
			for _, prePullContainer := range prePullInitContainers {
				g.Expect(regularContainerImages[prePullContainer.Image]).To(BeTrue(),
					"pre-pull init container %s uses image %s which is not in regular containers",
					prePullContainer.Name, prePullContainer.Image)
			}

			// Validate that pre-pull init containers do NOT use images from existing init containers
			existingInitImages := make(map[string]bool)
			for _, c := range tc.existingInitContainers {
				existingInitImages[c.Image] = true
			}
			for _, prePullContainer := range prePullInitContainers {
				g.Expect(existingInitImages[prePullContainer.Image]).To(BeFalse(),
					"pre-pull init container %s uses image %s which is already in an existing init container",
					prePullContainer.Name, prePullContainer.Image)
			}

			// Validate that pre-pull init containers come before other init containers
			if firstOtherInitContainerIndex != -1 && lastPrePullInitContainerIndex != -1 {
				g.Expect(lastPrePullInitContainerIndex).To(BeNumerically("<", firstOtherInitContainerIndex),
					"pre-pull init containers must come before other init containers")
			}
		})
	}
}

func TestAdaptDeployment_SwiftPodNetworkInstance(t *testing.T) {
	tests := []struct {
		name                    string
		hcp                     *hyperv1.HostedControlPlane
		wantSwiftLabel          bool
		expectedSwiftLabelValue string
	}{
		{
			name: "When Swift is enabled it should add the Swift pod network instance label",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						hyperv1.SwiftPodNetworkInstanceAnnotationCpo: "test-swift-instance",
					},
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
					},
					Configuration: &hyperv1.ClusterConfiguration{},
				},
			},
			wantSwiftLabel:          true,
			expectedSwiftLabelValue: "test-swift-instance",
		},
		{
			name: "When Swift annotation is not present it should not add the label",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
					},
					Configuration: &hyperv1.ClusterConfiguration{},
				},
			},
			wantSwiftLabel: false,
		},
		{
			name: "When Swift annotation is empty it should not add the label",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						hyperv1.SwiftPodNetworkInstanceAnnotationCpo: "",
					},
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
					},
					Configuration: &hyperv1.ClusterConfiguration{},
				},
			},
			wantSwiftLabel: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())

			cpContext := component.WorkloadContext{
				Context:                  t.Context(),
				HCP:                      tt.hcp,
				UserReleaseImageProvider: testutil.FakeImageProvider(),
			}

			err = adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			swiftLabel, exists := deployment.Spec.Template.Labels["kubernetes.azure.com/pod-network-instance"]
			if tt.wantSwiftLabel {
				g.Expect(exists).To(BeTrue(), "expected Swift pod network instance label to be set, but it was not found")
				g.Expect(swiftLabel).To(Equal(tt.expectedSwiftLabelValue))
			} else {
				g.Expect(exists).To(BeFalse(), "expected Swift pod network instance label to not be set, but it was found with value: %v", swiftLabel)
			}
		})
	}
}

func TestAdaptDeployment_SwiftDNSSidecarRemovesWaitForEtcd(t *testing.T) {
	tests := []struct {
		name                   string
		hcp                    *hyperv1.HostedControlPlane
		wantWaitForEtcdRemoved bool
	}{
		{
			name: "When Swift is enabled it should remove wait-for-etcd init container",
			hcp: &hyperv1.HostedControlPlane{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						hyperv1.SwiftPodNetworkInstanceAnnotationCpo: "swift-instance-1",
					},
				},
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
					},
					Etcd: hyperv1.EtcdSpec{
						ManagementType: hyperv1.Managed,
					},
					Configuration: &hyperv1.ClusterConfiguration{},
				},
			},
			wantWaitForEtcdRemoved: true,
		},
		{
			name: "When Swift is not enabled it should keep wait-for-etcd init container",
			hcp: &hyperv1.HostedControlPlane{
				Spec: hyperv1.HostedControlPlaneSpec{
					Platform: hyperv1.PlatformSpec{
						Type: hyperv1.AzurePlatform,
					},
					Etcd: hyperv1.EtcdSpec{
						ManagementType: hyperv1.Managed,
					},
					Configuration: &hyperv1.ClusterConfiguration{},
				},
			},
			wantWaitForEtcdRemoved: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewGomegaWithT(t)

			deployment, err := assets.LoadDeploymentManifest(ComponentName)
			g.Expect(err).ToNot(HaveOccurred())

			cpContext := component.WorkloadContext{
				Context:                  t.Context(),
				HCP:                      tt.hcp,
				UserReleaseImageProvider: testutil.FakeImageProvider(),
			}

			err = adaptDeployment(cpContext, deployment)
			g.Expect(err).ToNot(HaveOccurred())

			// Check if wait-for-etcd init container exists
			waitForEtcdExists := false
			for _, container := range deployment.Spec.Template.Spec.InitContainers {
				if container.Name == "wait-for-etcd" {
					waitForEtcdExists = true
					break
				}
			}

			if tt.wantWaitForEtcdRemoved {
				g.Expect(waitForEtcdExists).To(BeFalse(), "expected wait-for-etcd init container to be removed when Swift is enabled")
			} else {
				g.Expect(waitForEtcdExists).To(BeTrue(), "expected wait-for-etcd init container to exist when Swift is not enabled")
			}
		})
	}
}
