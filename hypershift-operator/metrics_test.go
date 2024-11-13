package main

import (
	"testing"
	"time"

	. "github.com/onsi/gomega"

	"github.com/openshift/hypershift/cmd/install/assets"
	"github.com/openshift/hypershift/support/api"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

const (
	fakePodName = "operator-fakepod"
)

func TestGetOperatorImage(t *testing.T) {
	testCases := []struct {
		name            string
		hypershiftPod   *corev1.Pod
		expectedImage   string
		expectedImageID string
		expectedError   bool
	}{
		{
			name: "When the pod is running it should get details from it",
			hypershiftPod: &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      fakePodName,
					Namespace: "hypershift",
				},
				Status: corev1.PodStatus{
					Conditions: []corev1.PodCondition{
						{
							Type:               corev1.PodReady,
							Status:             corev1.ConditionTrue,
							LastTransitionTime: metav1.Time{Time: time.Time{}.Add(10 * time.Minute)},
						},
					},
					ContainerStatuses: []corev1.ContainerStatus{
						{
							Name:    assets.HypershiftOperatorName,
							Image:   "quay.io/hypershift/hypershift:latest",
							ImageID: "quay.io/hypershift/hypershift@sha256:746dc6b979cd6d42d4e6f3214e20eb5a2e0a2b4d1c1345f6914e5492e69d9afe",
							State: corev1.ContainerState{
								Running: &corev1.ContainerStateRunning{
									StartedAt: metav1.Time{Time: time.Time{}},
								},
							},
						},
					},
				},
			},
			expectedImage:   "quay.io/hypershift/hypershift:latest",
			expectedImageID: "quay.io/hypershift/hypershift@sha256:746dc6b979cd6d42d4e6f3214e20eb5a2e0a2b4d1c1345f6914e5492e69d9afe",
		},
		{
			name:            "When the pod is not running it should return strings with 'not found' and an error",
			hypershiftPod:   &corev1.Pod{},
			expectedImage:   "not found",
			expectedImageID: "not found",
			expectedError:   true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MY_NAMESPACE", "hypershift")
			t.Setenv("MY_NAME", fakePodName)
			g := NewWithT(t)
			client := fake.NewClientBuilder().WithScheme(api.Scheme).WithObjects(tc.hypershiftPod).Build()
			image, imageId, err := getOperatorImage(client)
			if tc.expectedError {
				g.Expect(apierrors.IsNotFound(err)).To(BeTrue())
			}
			g.Expect(image).To(BeIdenticalTo(tc.expectedImage))
			g.Expect(imageId).To(BeIdenticalTo(tc.expectedImageID))
		})
	}
}
