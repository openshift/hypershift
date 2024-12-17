package olm

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/config"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestReconcilePackageServerDeployment(t *testing.T) {
	t.Run("Packageserver resource preservation", func(t *testing.T) {
		dep := &appsv1.Deployment{
			Spec: appsv1.DeploymentSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{
							{
								Name: packageServerName,
								Resources: corev1.ResourceRequirements{
									Requests: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("100m"),
										corev1.ResourceMemory: resource.MustParse("100Mi"),
									},
									Limits: corev1.ResourceList{
										corev1.ResourceCPU:    resource.MustParse("1000m"),
										corev1.ResourceMemory: resource.MustParse("1000Mi"),
									},
								},
							},
						},
					},
				},
			},
		}
		if err := ReconcilePackageServerDeployment(dep, config.OwnerRef{}, "", "", "", config.DeploymentConfig{}, "", []string{}, hyperv1.NonePlatform); err != nil {
			t.Fatalf("ReconcilePackageServerDeployment: %v", err)
		}

		// Verify the existing resources were preserved
		if dep.Spec.Template.Spec.Containers[0].Resources.Requests.Cpu().MilliValue() != 100 ||
			dep.Spec.Template.Spec.Containers[0].Resources.Requests.Memory().Value()/(1024*1024) != 100 ||
			dep.Spec.Template.Spec.Containers[0].Resources.Limits.Cpu().MilliValue() != 1000 ||
			dep.Spec.Template.Spec.Containers[0].Resources.Limits.Memory().Value()/(1024*1024) != 1000 {
			t.Error("some or all existing deployment resources were not preserved")
		}
	})
}
