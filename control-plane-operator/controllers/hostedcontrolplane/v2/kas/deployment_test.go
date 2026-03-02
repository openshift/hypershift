package kas

import (
	"strings"
	"testing"

	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
)

func TestAddImagePrePullInitContainers(t *testing.T) {
	testCases := []struct {
		name                  string
		containers            []corev1.Container
		expectedPrePullImages []string
	}{
		{
			name: "When kube-apiserver container exists it should pre-pull only the apiserver image",
			containers: []corev1.Container{
				{Name: "kube-apiserver", Image: "registry.io/kube-apiserver:v1"},
				{Name: "bootstrap", Image: "registry.io/controlplane-operator:v1"},
				{Name: "konnectivity-server", Image: "registry.io/konnectivity:v1"},
			},
			expectedPrePullImages: []string{"registry.io/kube-apiserver:v1"},
		},
		{
			name: "When kube-apiserver container does not exist it should not create pre-pull init containers",
			containers: []corev1.Container{
				{Name: "konnectivity-server", Image: "registry.io/konnectivity:v1"},
				{Name: "audit-logs", Image: "registry.io/cli:v1"},
			},
			expectedPrePullImages: []string{},
		},
		{
			name:                  "When there are no containers it should have no pre-pull init containers",
			containers:            []corev1.Container{},
			expectedPrePullImages: []string{},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewWithT(t)

			podSpec := &corev1.PodSpec{
				Containers: tc.containers,
			}

			addImagePrePullInitContainers(podSpec)

			// Find pre-pull init containers and their positions
			var prePullInitContainers []corev1.Container
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
			g.Expect(len(prePullInitContainers)).To(Equal(len(tc.expectedPrePullImages)),
				"unexpected number of pre-pull init containers")

			// Validate that pre-pull init containers use the expected images
			prePullImages := make([]string, 0, len(prePullInitContainers))
			for _, c := range prePullInitContainers {
				prePullImages = append(prePullImages, c.Image)
			}
			g.Expect(prePullImages).To(Equal(tc.expectedPrePullImages),
				"pre-pull init containers have unexpected images")

			// Validate that pre-pull init containers come before other init containers
			if firstOtherInitContainerIndex != -1 && lastPrePullInitContainerIndex != -1 {
				g.Expect(lastPrePullInitContainerIndex).To(BeNumerically("<", firstOtherInitContainerIndex),
					"pre-pull init containers must come before other init containers")
			}
		})
	}
}
