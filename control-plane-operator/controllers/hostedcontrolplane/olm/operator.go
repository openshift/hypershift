package olm

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
)

var (
	catalogOperatorMetricsService = MustService("assets/catalog-metrics-service.yaml")
	catalogOperatorDeployment     = MustDeployment("assets/catalog-operator-deployment.yaml")

	olmOperatorMetricsService = MustService("assets/olm-metrics-service.yaml")
	olmOperatorDeployment     = MustDeployment("assets/olm-operator-deployment.yaml")
)

func ReconcileCatalogOperatorMetricsService(svc *corev1.Service, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(svc)

	// The service is assigned a cluster IP when it is created.
	// This field is immutable as shown here: https://github.com/kubernetes/api/blob/62998e98c313b2ca15b1da278aa702bdd7b84cb0/core/v1/types.go#L4114-L4130
	// As such, to avoid an error when updating the object, only update the fields OLM specifies.
	catalogOperatorMetricsServiceDeepCopy := catalogOperatorMetricsService.DeepCopy()
	svc.Spec.Ports = catalogOperatorMetricsServiceDeepCopy.Spec.Ports
	svc.Spec.Type = catalogOperatorMetricsServiceDeepCopy.Spec.Type
	svc.Spec.Selector = catalogOperatorMetricsServiceDeepCopy.Spec.Selector
	return nil
}

func ReconcileCatalogOperatorDeployment(deployment *appsv1.Deployment, ownerRef config.OwnerRef, olmImage, socks5ProxyImage, operatorRegistryImage, releaseVersion string, dc config.DeploymentConfig, availabilityProberImage string, apiPort *int32) error {
	ownerRef.ApplyTo(deployment)
	deployment.Spec = catalogOperatorDeployment.DeepCopy().Spec
	for i, container := range deployment.Spec.Template.Spec.Containers {
		switch container.Name {
		case "catalog-operator":
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
		case "OLM_OPERATOR_IMAGE":
			deployment.Spec.Template.Spec.Containers[0].Env[i].Value = olmImage
		case "OPERATOR_REGISTRY_IMAGE":
			deployment.Spec.Template.Spec.Containers[0].Env[i].Value = operatorRegistryImage
		}
	}
	dc.ApplyTo(deployment)
	util.AvailabilityProber(kas.InClusterKASReadyURL(deployment.Namespace, apiPort), availabilityProberImage, &deployment.Spec.Template.Spec)
	return nil
}

func ReconcileOLMOperatorMetricsService(svc *corev1.Service, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(svc)

	// The service is assigned a cluster IP when it is created.
	// This field is immutable as shown here: https://github.com/kubernetes/api/blob/62998e98c313b2ca15b1da278aa702bdd7b84cb0/core/v1/types.go#L4114-L4130
	// As such, to avoid an error when updating the object, only update the fields OLM specifies.
	olmOperatorMetricsServiceDeepCopy := olmOperatorMetricsService.DeepCopy()
	svc.Spec.Ports = olmOperatorMetricsServiceDeepCopy.Spec.Ports
	svc.Spec.Type = olmOperatorMetricsServiceDeepCopy.Spec.Type
	svc.Spec.Selector = olmOperatorMetricsServiceDeepCopy.Spec.Selector

	return nil
}

func ReconcileOLMOperatorDeployment(deployment *appsv1.Deployment, ownerRef config.OwnerRef, olmImage, socks5ProxyImage, releaseVersion string, dc config.DeploymentConfig, availabilityProberImage string, apiPort *int32) error {
	ownerRef.ApplyTo(deployment)
	deployment.Spec = olmOperatorDeployment.DeepCopy().Spec
	for i, container := range deployment.Spec.Template.Spec.Containers {
		switch container.Name {
		case "olm-operator":
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
		}
	}
	dc.ApplyTo(deployment)
	util.AvailabilityProber(kas.InClusterKASReadyURL(deployment.Namespace, apiPort), availabilityProberImage, &deployment.Spec.Template.Spec)
	return nil
}
