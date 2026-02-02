package kas

import (
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
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
