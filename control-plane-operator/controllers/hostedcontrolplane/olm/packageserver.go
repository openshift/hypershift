package olm

import (
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/support/assets"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	packageServerName = "packageserver"
)

var (
	packageServerDeployment = assets.MustDeployment(content.ReadFile, "assets/packageserver-deployment.yaml")
)

func ReconcilePackageServerDeployment(deployment *appsv1.Deployment, ownerRef config.OwnerRef, olmImage, socks5ProxyImage, releaseVersion string, dc config.DeploymentConfig, availabilityProberImage string, noProxy []string, platformType hyperv1.PlatformType) error {
	ownerRef.ApplyTo(deployment)

	// preserve existing resource requirements
	packageserverResources := corev1.ResourceRequirements{}
	mainContainer := util.FindContainer(packageServerName, packageServerDeployment.Spec.Template.Spec.Containers)
	if mainContainer != nil {
		packageserverResources = mainContainer.Resources
	}
	mainContainer = util.FindContainer(packageServerName, deployment.Spec.Template.Spec.Containers)
	if mainContainer != nil {
		if len(mainContainer.Resources.Requests) > 0 || len(mainContainer.Resources.Limits) > 0 {
			packageserverResources = mainContainer.Resources
		}
	}

	deployment.Spec = packageServerDeployment.DeepCopy().Spec
	for i, container := range deployment.Spec.Template.Spec.Containers {
		switch container.Name {
		case packageServerName:
			deployment.Spec.Template.Spec.Containers[i].Image = olmImage
			deployment.Spec.Template.Spec.Containers[i].Resources = packageserverResources
		case "socks5-proxy":
			deployment.Spec.Template.Spec.Containers[i].Image = socks5ProxyImage
			deployment.Spec.Template.Spec.Containers[i].ImagePullPolicy = corev1.PullIfNotPresent
			deployment.Spec.Template.Spec.Containers[i].Resources.Requests = corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10m"),
				corev1.ResourceMemory: resource.MustParse("30Mi"),
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
	util.AvailabilityProber(kas.InClusterKASReadyURL(platformType), availabilityProberImage, &deployment.Spec.Template.Spec, func(o *util.AvailabilityProberOpts) {
		o.KubeconfigVolumeName = "kubeconfig"
		o.RequiredAPIs = []schema.GroupVersionKind{
			{Group: "operators.coreos.com", Version: "v1alpha1", Kind: "CatalogSource"},
		}
	})
	return nil
}
