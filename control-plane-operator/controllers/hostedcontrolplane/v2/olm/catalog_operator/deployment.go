package catalogoperator

import (
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	noProxy := []string{"kube-apiserver"}
	if cpContext.HCP.Spec.OLMCatalogPlacement == hyperv1.ManagementOLMCatalogPlacement {
		noProxy = append(noProxy, "certified-operators", "community-operators", "redhat-operators", "redhat-marketplace")
	}

	util.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		util.UpsertEnvVars(c, []corev1.EnvVar{
			{Name: "RELEASE_VERSION", Value: cpContext.UserReleaseImageProvider.Version()},
			{Name: "OLM_OPERATOR_IMAGE", Value: cpContext.ReleaseImageProvider.GetImage("operator-lifecycle-manager")},
			{Name: "OPERATOR_REGISTRY_IMAGE", Value: cpContext.ReleaseImageProvider.GetImage("operator-registry")},
			{Name: "NO_PROXY", Value: strings.Join(noProxy, ",")},
		})
	})
	return nil
}
