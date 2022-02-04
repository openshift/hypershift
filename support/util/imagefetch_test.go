package util

import (
	"context"
	"github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/support/releaseinfo"
	fakereleaseprovider "github.com/openshift/hypershift/support/releaseinfo/fake"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	"testing"
)

func TestGetHypershiftComponentImage(t *testing.T) {
	fakeReleaseImage := "myimage:1"
	fakePullSecret := []byte(`pullsecret`)
	fakeReleaseProvider := &fakereleaseprovider.FakeReleaseProvider{}
	testsCases := []struct {
		name                         string
		inputAnnotations             map[string]string
		inputReleaseImage            string
		inputReleaseProvider         releaseinfo.Provider
		inputHypershiftOperatorImage string
		inputPullSecret              []byte
		expectedImage                string
	}{
		{
			name:                         "hypershift image is used if no annotation specified",
			inputAnnotations:             nil,
			inputReleaseImage:            fakeReleaseImage,
			inputReleaseProvider:         fakeReleaseProvider,
			inputHypershiftOperatorImage: "image1",
			inputPullSecret:              fakePullSecret,
			expectedImage:                "image1",
		},
		{
			name: "Image override annotation is used when specified",
			inputAnnotations: map[string]string{
				hyperv1.ControlPlaneOperatorImageAnnotation: "image2",
			},
			inputReleaseImage:            fakeReleaseImage,
			inputReleaseProvider:         fakeReleaseProvider,
			inputHypershiftOperatorImage: "image1",
			inputPullSecret:              fakePullSecret,
			expectedImage:                "image2",
		},
	}
	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := gomega.NewGomegaWithT(t)
			componentImage, err := GetHypershiftComponentImage(context.Background(), tc.inputAnnotations, tc.inputReleaseImage, tc.inputReleaseProvider, tc.inputHypershiftOperatorImage, tc.inputPullSecret)
			g.Expect(err).To(gomega.BeNil())
			g.Expect(componentImage).To(gomega.BeEquivalentTo(tc.expectedImage))
		})
	}
}

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
		inputPodName       string
		inputContainerName string
		expectedImage      string
		expectedError      bool
	}{
		{
			name:               "valid pod and container returns the associated image",
			inputResources:     fakeInputObjects,
			inputPodName:       fakePodName,
			inputContainerName: containerToFindName,
			expectedImage:      containerToFindImage,
			expectedError:      false,
		},
		{
			name:               "invalid pod returns an error",
			inputResources:     fakeInputObjects,
			inputPodName:       "random-name-name",
			inputContainerName: containerToFindName,
			expectedImage:      "",
			expectedError:      true,
		},
		{
			name:               "invalid container name returns an error",
			inputResources:     fakeInputObjects,
			inputPodName:       fakePodName,
			inputContainerName: "invalid-name",
			expectedImage:      "",
			expectedError:      true,
		},
	}
	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := gomega.NewGomegaWithT(t)
			client := fake.NewSimpleClientset(tc.inputResources...)
			activeImage, err := LookupActiveContainerImage(context.Background(), client.CoreV1().Pods(fakeNamespace), tc.inputPodName, tc.inputContainerName)
			if tc.expectedError {
				g.Expect(err).ToNot(gomega.BeNil())
			} else {
				g.Expect(err).To(gomega.BeNil())
				g.Expect(activeImage).To(gomega.BeEquivalentTo(tc.expectedImage))
			}
		})
	}
}
