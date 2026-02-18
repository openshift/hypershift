package packageserver

import (
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	hcp := cpContext.HCP

	noProxy := []string{"kube-apiserver"}
	if hcp.Spec.OLMCatalogPlacement == hyperv1.ManagementOLMCatalogPlacement {
		noProxy = append(noProxy, "certified-operators", "community-operators", "redhat-operators", "redhat-marketplace")
	}

	util.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		util.UpsertEnvVars(c, []corev1.EnvVar{
			{Name: "RELEASE_VERSION", Value: cpContext.UserReleaseImageProvider.Version()},
			{Name: "NO_PROXY", Value: strings.Join(noProxy, ",")},
		})
	})

	if hcp.Spec.Platform.Type == hyperv1.IBMCloudPlatform && hcp.Spec.ControllerAvailabilityPolicy == hyperv1.HighlyAvailable {
		deployment.Spec.Replicas = ptr.To[int32](2)
	}

	kasLivezURL := kas.InClusterKASURL(hcp.Spec.Platform.Type) + "/livez"
	deployment.Spec.Template.Spec.Containers = append(
		deployment.Spec.Template.Spec.Containers,
		util.KASReadinessCheckContainer(kasLivezURL),
	)

	return nil
}
