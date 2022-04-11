package util

import (
	"testing"

	. "github.com/onsi/gomega"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	v12 "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestSetRestartAnnotation(t *testing.T) {
	fakeReleaseImage := "registry.com/namespace/image:1"
	testsCases := []struct {
		name               string
		inputDeployment    *appsv1.Deployment
		expectedDeployment *appsv1.Deployment
	}{
		{
			name: "when release image is specified it is populated on the deployment",
			inputDeployment: &appsv1.Deployment{
				ObjectMeta: v1.ObjectMeta{
					Name:      "deploy1",
					Namespace: "master",
				},
			},
			expectedDeployment: &appsv1.Deployment{
				ObjectMeta: v1.ObjectMeta{
					Name:      "deploy1",
					Namespace: "master",
					Annotations: map[string]string{
						hyperv1.ReleaseImageAnnotation: fakeReleaseImage,
					},
				},
				Spec: appsv1.DeploymentSpec{
					Template: v12.PodTemplateSpec{
						ObjectMeta: v1.ObjectMeta{
							Annotations: map[string]string{
								hyperv1.ReleaseImageAnnotation: fakeReleaseImage,
							},
						},
					},
				},
			},
		},
	}
	for _, tc := range testsCases {
		t.Run(tc.name, func(t *testing.T) {
			g := NewGomegaWithT(t)
			SetReleaseImageAnnotation(tc.inputDeployment, fakeReleaseImage)
			g.Expect(tc.inputDeployment).To(BeEquivalentTo(tc.expectedDeployment))
		})
	}
}
