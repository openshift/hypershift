package snapshotcontroller

import (
	"context"
	"errors"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/imageprovider"
	component "github.com/openshift/hypershift/support/controlplane-component"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestIsStorageAndCSIManaged(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name     string
		platform hyperv1.PlatformType
		expected bool
	}{
		{
			name:     "When platform is IBMCloud, it should return false",
			platform: hyperv1.IBMCloudPlatform,
			expected: false,
		},
		{
			name:     "When platform is PowerVS, it should return false",
			platform: hyperv1.PowerVSPlatform,
			expected: false,
		},
		{
			name:     "When platform is AWS, it should return true",
			platform: hyperv1.AWSPlatform,
			expected: true,
		},
		{
			name:     "When platform is Azure, it should return true",
			platform: hyperv1.AzurePlatform,
			expected: true,
		},
		{
			name:     "When platform is KubeVirt, it should return true",
			platform: hyperv1.KubevirtPlatform,
			expected: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			cpContext := component.WorkloadContext{
				HCP: &hyperv1.HostedControlPlane{
					Spec: hyperv1.HostedControlPlaneSpec{
						Platform: hyperv1.PlatformSpec{
							Type: tc.platform,
						},
					},
				},
			}

			result, err := isStorageAndCSIManaged(cpContext)
			g.Expect(err).ToNot(HaveOccurred())
			g.Expect(result).To(Equal(tc.expected))
		})
	}
}

func buildDeployment(image string, ready bool) *appsv1.Deployment {
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:       "csi-snapshot-controller",
			Namespace:  "test-ns",
			Generation: 1,
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: ptr.To[int32](1),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "snapshot-controller",
							Image: image,
						},
					},
				},
			},
		},
	}

	if ready {
		deployment.Status = appsv1.DeploymentStatus{
			Replicas:           1,
			UpdatedReplicas:    1,
			AvailableReplicas:  1,
			ReadyReplicas:      1,
			ObservedGeneration: 1,
		}
	} else {
		deployment.Status = appsv1.DeploymentStatus{
			Replicas:          1,
			UpdatedReplicas:   0,
			AvailableReplicas: 0,
		}
	}

	return deployment
}

func TestCheckOperandsRolloutStatus(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	_ = appsv1.AddToScheme(scheme)
	_ = corev1.AddToScheme(scheme)
	_ = hyperv1.AddToScheme(scheme)

	testCases := []struct {
		name          string
		deployment    *appsv1.Deployment
		expectedImage string
		expectedError error
	}{
		{
			name:          "When deployment does not exist, it should return error",
			deployment:    nil,
			expectedImage: "test-image:v1",
			expectedError: errors.New("failed to get deployment csi-snapshot-controller"),
		},
		{
			name:          "When container image does not match expected image, it should return error",
			deployment:    buildDeployment("wrong-image:v1", false),
			expectedImage: "test-image:v1",
			expectedError: errors.New("container snapshot-controller in deployment csi-snapshot-controller is not using the expected image test-image:v1"),
		},
		{
			name:          "When deployment exists with correct image but is not ready, it should return error",
			deployment:    buildDeployment("test-image:v1", false),
			expectedImage: "test-image:v1",
			expectedError: errors.New("deployment csi-snapshot-controller is not ready"),
		},
		{
			name:          "When deployment exists with correct image and is ready, it should return true",
			deployment:    buildDeployment("test-image:v1", true),
			expectedImage: "test-image:v1",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			g := NewWithT(t)

			var objects []runtime.Object
			if tc.deployment != nil {
				objects = append(objects, tc.deployment)
			}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(objects...).
				Build()

			imageProvider := imageprovider.NewFromImages(map[string]string{
				"csi-snapshot-controller": tc.expectedImage,
			})

			cpContext := component.WorkloadContext{
				Context: context.TODO(),
				HCP: &hyperv1.HostedControlPlane{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-hcp",
						Namespace: "test-ns",
					},
				},
				Client:               fakeClient,
				ReleaseImageProvider: imageProvider,
			}

			ready, err := checkOperandsRolloutStatus(cpContext)

			if tc.expectedError != nil {
				g.Expect(err).To(HaveOccurred())
				g.Expect(err.Error()).To(ContainSubstring(tc.expectedError.Error()))
				g.Expect(ready).To(BeFalse())
			} else {
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(ready).To(BeTrue())
			}
		})
	}
}
