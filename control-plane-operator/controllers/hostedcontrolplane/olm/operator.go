package olm

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/util"
)

var (
	catalogOperatorMetricsService = MustService("assets/catalog-metrics-service.yaml")
	catalogOperatorDeployment     = MustDeployment("assets/catalog-operator-deployment.yaml")

	olmOperatorMetricsService = MustService("assets/olm-metrics-service.yaml")
	olmOperatorDeployment     = MustDeployment("assets/olm-operator-deployment.yaml")

	olmAlertRules = MustAsset("assets/prometheus-rule-guest.yaml")
)

func ReconcileCatalogOperatorMetricsService(svc *corev1.Service, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(svc)
	svc.Spec = catalogOperatorMetricsService.DeepCopy().Spec
	return nil
}

func ReconcileCatalogOperatorDeployment(deployment *appsv1.Deployment, ownerRef config.OwnerRef, olmImage, operatorRegistryImage, releaseVersion string, dc config.DeploymentConfig) error {
	ownerRef.ApplyTo(deployment)
	deployment.Spec = catalogOperatorDeployment.DeepCopy().Spec
	deployment.Spec.Template.Spec.Containers[0].Image = olmImage
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
	return nil
}

func ReconcileOLMOperatorMetricsService(svc *corev1.Service, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(svc)
	svc.Spec = olmOperatorMetricsService.DeepCopy().Spec
	return nil
}

func ReconcileOLMOperatorDeployment(deployment *appsv1.Deployment, ownerRef config.OwnerRef, olmImage, releaseVersion string, dc config.DeploymentConfig) error {
	ownerRef.ApplyTo(deployment)
	deployment.Spec = olmOperatorDeployment.DeepCopy().Spec
	deployment.Spec.Template.Spec.Containers[0].Image = olmImage
	for i, env := range deployment.Spec.Template.Spec.Containers[0].Env {
		switch env.Name {
		case "RELEASE_VERSION":
			deployment.Spec.Template.Spec.Containers[0].Env[i].Value = releaseVersion
		}
	}
	dc.ApplyTo(deployment)
	return nil
}

func ReconcileOLMWorkerPrometheusRulesManifest(cm *corev1.ConfigMap, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(cm)
	return util.ReconcileWorkerManifestString(cm, string(olmAlertRules))
}
