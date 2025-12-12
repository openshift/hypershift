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
		},
		{
			name: "When there are no existing init containers it should only have pre-pull init containers",
			containers: []corev1.Container{
				{Name: "kube-apiserver", Image: "registry.io/kube-apiserver:v1"},
			},
			existingInitContainers: []corev1.Container{},
		},
		{
			// edge case that should never happen, still testing for robustness
			name:                   "When there are no existing containers it should have no pre-pull init containers",
			containers:             []corev1.Container{},
			existingInitContainers: []corev1.Container{},
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

			// Validate we have one pre-pull init container per unique regular container image
			g.Expect(len(prePullInitContainers)).To(Equal(len(regularContainerImages)),
				"expected one pre-pull init container per unique regular container image")

			// Validate that pre-pull init containers use images from regular containers
			for _, prePullContainer := range prePullInitContainers {
				g.Expect(regularContainerImages[prePullContainer.Image]).To(BeTrue(),
					"pre-pull init container %s uses image %s which is not in regular containers",
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
