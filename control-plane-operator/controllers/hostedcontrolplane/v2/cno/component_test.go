package cno

import (
	"context"
	"testing"

	. "github.com/onsi/gomega"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestSetRestartAnnotationAndPatch(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = appsv1.AddToScheme(scheme)

	tests := []struct {
		name              string
		existingDeploy    *appsv1.Deployment
		restartAnnotation string
		expectError       bool
		expectAnnotation  bool
	}{
		{
			name: "When deployment exists with nil annotations it should set the restart annotation",
			existingDeploy: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test-ns",
					Name:      "test-deploy",
				},
			},
			restartAnnotation: "2026-06-15T12:00:00Z",
			expectAnnotation:  true,
		},
		{
			name: "When deployment exists with existing annotations it should set the restart annotation",
			existingDeploy: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test-ns",
					Name:      "test-deploy",
				},
				Spec: appsv1.DeploymentSpec{
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Annotations: map[string]string{
								"existing-key": "existing-value",
							},
						},
					},
				},
			},
			restartAnnotation: "2026-06-15T12:00:00Z",
			expectAnnotation:  true,
		},
		{
			name:              "When deployment does not exist it should return nil",
			existingDeploy:    nil,
			restartAnnotation: "2026-06-15T12:00:00Z",
			expectAnnotation:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)

			builder := fake.NewClientBuilder().WithScheme(scheme)
			if tt.existingDeploy != nil {
				builder = builder.WithObjects(tt.existingDeploy)
			}
			cl := builder.Build()

			dep := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "test-ns",
					Name:      "test-deploy",
				},
			}

			err := SetRestartAnnotationAndPatch(context.Background(), cl, dep, tt.restartAnnotation)
			if tt.expectError {
				g.Expect(err).To(HaveOccurred())
				return
			}
			g.Expect(err).ToNot(HaveOccurred())

			if tt.expectAnnotation {
				updated := &appsv1.Deployment{}
				err = cl.Get(context.Background(), client.ObjectKeyFromObject(dep), updated)
				g.Expect(err).ToNot(HaveOccurred())
				g.Expect(updated.Spec.Template.ObjectMeta.Annotations).To(HaveKeyWithValue(hyperv1.RestartDateAnnotation, tt.restartAnnotation))
			}
		})
	}
}

func TestPlatformHasCloudNetworkConfigController(t *testing.T) {
	tests := []struct {
		name         string
		platformType hyperv1.PlatformType
		expected     bool
	}{
		{
			name:         "When platform is AWS it should have cloud-network-config-controller",
			platformType: hyperv1.AWSPlatform,
			expected:     true,
		},
		{
			name:         "When platform is Azure it should have cloud-network-config-controller",
			platformType: hyperv1.AzurePlatform,
			expected:     true,
		},
		{
			name:         "When platform is GCP it should have cloud-network-config-controller",
			platformType: hyperv1.GCPPlatform,
			expected:     true,
		},
		{
			name:         "When platform is OpenStack it should have cloud-network-config-controller",
			platformType: hyperv1.OpenStackPlatform,
			expected:     true,
		},
		{
			name:         "When platform is KubeVirt it should not have cloud-network-config-controller",
			platformType: hyperv1.KubevirtPlatform,
			expected:     false,
		},
		{
			name:         "When platform is Agent it should not have cloud-network-config-controller",
			platformType: hyperv1.AgentPlatform,
			expected:     false,
		},
		{
			name:         "When platform is None it should not have cloud-network-config-controller",
			platformType: hyperv1.NonePlatform,
			expected:     false,
		},
		{
			name:         "When platform is IBMCloud it should not have cloud-network-config-controller",
			platformType: hyperv1.IBMCloudPlatform,
			expected:     false,
		},
		{
			name:         "When platform is PowerVS it should not have cloud-network-config-controller",
			platformType: hyperv1.PowerVSPlatform,
			expected:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := NewWithT(t)
			result := platformHasCloudNetworkConfigController(tt.platformType)
			g.Expect(result).To(Equal(tt.expected))
		})
	}
}
