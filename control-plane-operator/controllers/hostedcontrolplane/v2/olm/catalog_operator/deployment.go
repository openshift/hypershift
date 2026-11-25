package catalogoperator

import (
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	noProxy := []string{"kube-apiserver"}
	if cpContext.HCP.Spec.OLMCatalogPlacement == hyperv1.ManagementOLMCatalogPlacement {
		noProxy = append(noProxy, "certified-operators", "community-operators", "redhat-operators")
	}

	podspec.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		podspec.UpsertEnvVars(c, []corev1.EnvVar{
			{Name: "RELEASE_VERSION", Value: cpContext.UserReleaseImageProvider.Version()},
			// These do not set this operator's own image, which comes from the
			// control plane release via the deployment asset. They feed --util-image,
			// --opmImage and --configmapServerImage, which catalog-operator stamps into
			// workloads it creates in the guest cluster: bundle unpack Jobs, grpc
			// CatalogSource pods using extractContent and ConfigMap-backed
			// CatalogSource pods. Those run on guest nodes, so the images must come
			// from the user release; the control plane release may be held in a
			// private registry the guest cannot pull from.
			{Name: "OLM_OPERATOR_IMAGE", Value: cpContext.UserReleaseImageProvider.GetImage("operator-lifecycle-manager")},
			{Name: "OPERATOR_REGISTRY_IMAGE", Value: cpContext.UserReleaseImageProvider.GetImage("operator-registry")},
			{Name: "NO_PROXY", Value: strings.Join(noProxy, ",")},
		})
	})
	return nil
}
