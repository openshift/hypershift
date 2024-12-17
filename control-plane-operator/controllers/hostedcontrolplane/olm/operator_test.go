package olm

import (
	"testing"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperapi "github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/testutil"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

func TestReconcileCatalogOperatorDeployment(t *testing.T) {
	tcs := []struct {
		name        string
		coResources *corev1.ResourceRequirements
	}{
		{
			name: "Preserve existing resources",
			coResources: &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("500m"),
					corev1.ResourceMemory: resource.MustParse("500Mi"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("1000m"),
					corev1.ResourceMemory: resource.MustParse("1000Mi"),
				},
			},
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			dep := &appsv1.Deployment{}
			if tc.coResources != nil {
				dep.Spec.Template.Spec.Containers = []corev1.Container{
					{
						Name:      catalogOperatorName,
						Resources: *tc.coResources,
					},
				}
			}

			if err := ReconcileCatalogOperatorDeployment(dep, config.OwnerRef{}, "", "", "", "", config.DeploymentConfig{}, "", []string{}, hyperv1.NonePlatform); err != nil {
				t.Fatalf("ReconcileCatalogOperatorDeployment: %v", err)
			}

			deploymentYaml, err := util.SerializeResource(dep, hyperapi.Scheme)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			testutil.CompareWithFixture(t, deploymentYaml)
		})
	}
}

func TestReconcileOLMOperatorDeployment(t *testing.T) {
	tcs := []struct {
		name           string
		olmOpResources *corev1.ResourceRequirements
	}{
		{
			name: "Preserve existing resources",
			olmOpResources: &corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("500m"),
					corev1.ResourceMemory: resource.MustParse("500Mi"),
				},
				Limits: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("1000m"),
					corev1.ResourceMemory: resource.MustParse("1000Mi"),
				},
			},
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			dep := &appsv1.Deployment{}
			if tc.olmOpResources != nil {
				dep.Spec.Template.Spec.Containers = []corev1.Container{
					{
						Name:      olmOperatorName,
						Resources: *tc.olmOpResources,
					},
				}
			}

			if err := ReconcileOLMOperatorDeployment(dep, config.OwnerRef{}, "", "", "", config.DeploymentConfig{}, "", []string{}, hyperv1.NonePlatform); err != nil {
				t.Fatalf("ReconcileOLMOperatorDeployment: %v", err)
			}

			deploymentYaml, err := util.SerializeResource(dep, hyperapi.Scheme)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			testutil.CompareWithFixture(t, deploymentYaml)
		})
	}
}
