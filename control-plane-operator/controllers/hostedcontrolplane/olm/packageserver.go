package olm

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/pki"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/util"
)

var (
	packageServerDeployment = MustDeployment("assets/packageserver-deployment.yaml")
	packageServerAPIService = MustAPIService("assets/packageserver-apiservice.yaml")
	packageServerService    = MustService("assets/packageserver-service-guest.yaml")
	packageServerEndpoints  = MustEndpoints("assets/packageserver-endpoint-guest.yaml")
)

func ReconcilePackageServerDeployment(deployment *appsv1.Deployment, ownerRef config.OwnerRef, olmImage, socks5ProxyImage, releaseVersion string, dc config.DeploymentConfig, availabilityProberImage string, apiPort *int32) error {
	ownerRef.ApplyTo(deployment)
	deployment.Spec = packageServerDeployment.DeepCopy().Spec
	for i, container := range deployment.Spec.Template.Spec.Containers {
		switch container.Name {
		case "packageserver":
			deployment.Spec.Template.Spec.Containers[i].Image = olmImage
		case "socks5-proxy":
			deployment.Spec.Template.Spec.Containers[i].Image = socks5ProxyImage
			deployment.Spec.Template.Spec.Containers[i].ImagePullPolicy = corev1.PullAlways
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

func ReconcilePackageServerWorkerAPIServiceManifest(cm *corev1.ConfigMap, ownerRef config.OwnerRef, ca *corev1.Secret) error {
	ownerRef.ApplyTo(cm)
	apiService := packageServerAPIService.DeepCopy()
	apiService.Spec.CABundle = ca.Data[pki.CASignerCertMapKey]
	return util.ReconcileWorkerManifest(cm, apiService)
}

func ReconcilePackageServerWorkerServiceManifest(cm *corev1.ConfigMap, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(cm)
	svc := packageServerService.DeepCopy()
	return util.ReconcileWorkerManifest(cm, svc)
}

func ReconcilePackageServerWorkerEndpointsManifest(cm *corev1.ConfigMap, ownerRef config.OwnerRef, serviceIP string) error {
	ownerRef.ApplyTo(cm)
	ep := packageServerEndpoints.DeepCopy()
	ep.Subsets[0].Addresses = []corev1.EndpointAddress{
		{
			IP: serviceIP,
		},
	}
	return util.ReconcileWorkerManifest(cm, ep)
}
