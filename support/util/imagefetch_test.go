package util

import (
	"context"
	"github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"testing"
)

func TestLookupActiveContainerImage(t *testing.T) {
	fakePodName := "mypod"
	fakeNamespace := "mynamespace"
	containerToFindName := "container-to-find"
	containerToFindImage := "container-to-find-image"
	fakeInputObjects := []runtime.Object{
		&corev1.Pod{
			ObjectMeta: v1.ObjectMeta{
				Name:      fakePodName,
				Namespace: fakeNamespace,
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "random-container",
						Image: "random-image",
					},
					{
						Name:  containerToFindName,
						Image: containerToFindImage,
					},
				},
			},
		},
	}
	testsCases := []struct {
		name               string
		inputResources     []runtime.Object
		inputPod           *corev1.Pod
		inputContainerName string
		expectedImage      string
		expectedError      bool
	}{
		{
			name:           "valid pod and container returns the associated image",
			inputResources: fakeInputObjects,
			inputPod: &corev1.Pod{
				ObjectMeta: v1.ObjectMeta{
					Name:      fakePodName,
					Namespace: fakeNamespace,
				},
			},
			inputContainerName: containerToFindName,
			expectedImage:      containerToFindImage,
			expectedError:      false,
		},
		{
			name:           "invalid pod returns an error",
			inputResources: fakeInputObjects,
			inputPod: &corev1.Pod{
				ObjectMeta: v1.ObjectMeta{
					Name:      "random-name",
					Namespace: fakeNamespace,
				},
			},
			inputContainerName: containerToFindName,
			expectedImage:      "",
			expectedError:      true,
		},
		{
			name:           "invalid container name returns an error",
			inputResources: fakeInputObjects,
			inputPod: &corev1.Pod{
				ObjectMeta: v1.ObjectMeta{
					Name:      fakePodName,
					Namespace: fakeNamespace,
				},
			},
			inputContainerName: "invalid-name",
			expectedImage:      "",
			expectedError:      true,
		},
	}
	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := gomega.NewGomegaWithT(t)
			client := fake.NewClientBuilder().WithRuntimeObjects(tc.inputResources...).Build()
			activeImage, err := LookupActiveContainerImage(context.Background(), client, tc.inputPod, tc.inputContainerName)
			if tc.expectedError {
				g.Expect(err).ToNot(gomega.BeNil())
			} else {
				g.Expect(err).To(gomega.BeNil())
				g.Expect(activeImage).To(gomega.BeEquivalentTo(tc.expectedImage))
			}
		})
	}
}
