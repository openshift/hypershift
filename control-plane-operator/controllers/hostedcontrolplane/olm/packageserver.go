package olm

import (
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
)

var (
	packageServerDeployment = MustDeployment("assets/packageserver-deployment.yaml")
)

func ReconcilePackageServerDeployment(deployment *appsv1.Deployment, ownerRef config.OwnerRef, olmImage, socks5ProxyImage, releaseVersion string, dc config.DeploymentConfig, availabilityProberImage string, apiPort *int32, noProxy []string) error {
	ownerRef.ApplyTo(deployment)
	deployment.Spec = packageServerDeployment.DeepCopy().Spec
	for i, container := range deployment.Spec.Template.Spec.Containers {
		switch container.Name {
		case "packageserver":
			deployment.Spec.Template.Spec.Containers[i].Image = olmImage
		case "socks5-proxy":
			deployment.Spec.Template.Spec.Containers[i].Image = socks5ProxyImage
			deployment.Spec.Template.Spec.Containers[i].ImagePullPolicy = corev1.PullAlways
			deployment.Spec.Template.Spec.Containers[i].Resources.Requests = corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10m"),
				corev1.ResourceMemory: resource.MustParse("15Mi"),
			}
		}
	}
	for i, env := range deployment.Spec.Template.Spec.Containers[0].Env {
		switch env.Name {
		case "RELEASE_VERSION":
			deployment.Spec.Template.Spec.Containers[0].Env[i].Value = releaseVersion
		case "NO_PROXY":
			deployment.Spec.Template.Spec.Containers[0].Env[i].Value = strings.Join(noProxy, ",")
		}
	}
	dc.ApplyTo(deployment)
	util.AvailabilityProber(kas.InClusterKASReadyURL(deployment.Namespace, apiPort), availabilityProberImage, &deployment.Spec.Template.Spec, func(o *util.AvailabilityProberOpts) {
		o.KubeconfigVolumeName = "kubeconfig"
		o.RequiredAPIs = []schema.GroupVersionKind{
			{Group: "operators.coreos.com", Version: "v1alpha1", Kind: "CatalogSource"},
		}
	})
	return nil
}
