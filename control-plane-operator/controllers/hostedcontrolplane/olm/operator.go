package olm

import (
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/assets"
	"github.com/openshift/hypershift/support/certs"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/metrics"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

const (
	catalogOperatorName = "catalog-operator"
	olmOperatorName     = "olm-operator"
)

var (
	catalogOperatorMetricsService = assets.MustService(content.ReadFile, "assets/catalog-metrics-service.yaml")
	catalogOperatorDeployment     = assets.MustDeployment(content.ReadFile, "assets/catalog-operator-deployment.yaml")

	olmOperatorMetricsService = assets.MustService(content.ReadFile, "assets/olm-metrics-service.yaml")
	olmOperatorDeployment     = assets.MustDeployment(content.ReadFile, "assets/olm-operator-deployment.yaml")
)

func olmOperatorLabels() map[string]string {
	return map[string]string{"app": olmOperatorName, hyperv1.ControlPlaneComponentLabel: olmOperatorName}
}

func ReconcileCatalogOperatorMetricsService(svc *corev1.Service, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(svc)

	// The service is assigned a cluster IP when it is created.
	// This field is immutable as shown here: https://github.com/kubernetes/api/blob/62998e98c313b2ca15b1da278aa702bdd7b84cb0/core/v1/types.go#L4114-L4130
	// As such, to avoid an error when updating the object, only update the fields OLM specifies.
	catalogOperatorMetricsServiceDeepCopy := catalogOperatorMetricsService.DeepCopy()
	svc.Labels = catalogOperatorMetricsServiceDeepCopy.Labels
	svc.Spec.Ports = catalogOperatorMetricsServiceDeepCopy.Spec.Ports
	svc.Spec.Type = catalogOperatorMetricsServiceDeepCopy.Spec.Type
	svc.Spec.Selector = catalogOperatorMetricsServiceDeepCopy.Spec.Selector
	return nil
}

func ReconcileCatalogOperatorDeployment(deployment *appsv1.Deployment, ownerRef config.OwnerRef, olmImage, socks5ProxyImage, operatorRegistryImage, releaseVersion string, dc config.DeploymentConfig, availabilityProberImage string, noProxy []string, platformType hyperv1.PlatformType) error {
	ownerRef.ApplyTo(deployment)

	// preserve existing resource requirements
	catalogOperatorResources := corev1.ResourceRequirements{}
	mainContainer := util.FindContainer(catalogOperatorName, catalogOperatorDeployment.Spec.Template.Spec.Containers)
	if mainContainer != nil {
		catalogOperatorResources = mainContainer.Resources
	}
	mainContainer = util.FindContainer(catalogOperatorName, deployment.Spec.Template.Spec.Containers)
	if mainContainer != nil {
		if len(mainContainer.Resources.Requests) > 0 || len(mainContainer.Resources.Limits) > 0 {
			catalogOperatorResources = mainContainer.Resources
		}
	}

	deployment.Spec = catalogOperatorDeployment.DeepCopy().Spec
	for i, container := range deployment.Spec.Template.Spec.Containers {
		switch container.Name {
		case catalogOperatorName:
			deployment.Spec.Template.Spec.Containers[i].Image = olmImage
			deployment.Spec.Template.Spec.Containers[i].Resources = catalogOperatorResources
		case "socks5-proxy":
			deployment.Spec.Template.Spec.Containers[i].Image = socks5ProxyImage
			deployment.Spec.Template.Spec.Containers[i].ImagePullPolicy = corev1.PullIfNotPresent
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

func ReconcileOLMOperatorMetricsService(svc *corev1.Service, ownerRef config.OwnerRef) error {
	ownerRef.ApplyTo(svc)

	// The service is assigned a cluster IP when it is created.
	// This field is immutable as shown here: https://github.com/kubernetes/api/blob/62998e98c313b2ca15b1da278aa702bdd7b84cb0/core/v1/types.go#L4114-L4130
	// As such, to avoid an error when updating the object, only update the fields OLM specifies.
	olmOperatorMetricsServiceDeepCopy := olmOperatorMetricsService.DeepCopy()
	svc.Labels = olmOperatorMetricsService.Labels
	svc.Spec.Ports = olmOperatorMetricsServiceDeepCopy.Spec.Ports
	svc.Spec.Type = olmOperatorMetricsServiceDeepCopy.Spec.Type
	svc.Spec.Selector = olmOperatorMetricsServiceDeepCopy.Spec.Selector

	return nil
}

func ReconcileOLMOperatorDeployment(deployment *appsv1.Deployment, ownerRef config.OwnerRef, olmImage, socks5ProxyImage, releaseVersion string, dc config.DeploymentConfig, availabilityProberImage string, noProxy []string, platformType hyperv1.PlatformType) error {
	ownerRef.ApplyTo(deployment)

	// preserve existing resource requirements
	olmOperatorResources := corev1.ResourceRequirements{}
	mainContainer := util.FindContainer(olmOperatorName, olmOperatorDeployment.Spec.Template.Spec.Containers)
	if mainContainer != nil {
		olmOperatorResources = mainContainer.Resources
	}
	mainContainer = util.FindContainer(olmOperatorName, deployment.Spec.Template.Spec.Containers)
	if mainContainer != nil {
		if len(mainContainer.Resources.Requests) > 0 || len(mainContainer.Resources.Limits) > 0 {
			olmOperatorResources = mainContainer.Resources
		}
	}

	deployment.Spec = olmOperatorDeployment.DeepCopy().Spec
	for i, container := range deployment.Spec.Template.Spec.Containers {
		switch container.Name {
		case olmOperatorName:
			deployment.Spec.Template.Spec.Containers[i].Image = olmImage
			deployment.Spec.Template.Spec.Containers[i].Resources = olmOperatorResources
		case "socks5-proxy":
			deployment.Spec.Template.Spec.Containers[i].Image = socks5ProxyImage
			deployment.Spec.Template.Spec.Containers[i].ImagePullPolicy = corev1.PullIfNotPresent
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
	util.AvailabilityProber(kas.InClusterKASReadyURL(platformType), availabilityProberImage, &deployment.Spec.Template.Spec, func(o *util.AvailabilityProberOpts) {
		o.KubeconfigVolumeName = "kubeconfig"
		o.RequiredAPIs = []schema.GroupVersionKind{
			{Group: "operators.coreos.com", Version: "v1alpha1", Kind: "CatalogSource"},
			{Group: "operators.coreos.com", Version: "v1alpha1", Kind: "Subscription"},
			{Group: "operators.coreos.com", Version: "v2", Kind: "OperatorCondition"},
			{Group: "operators.coreos.com", Version: "v1", Kind: "OperatorGroup"},
			{Group: "operators.coreos.com", Version: "v1", Kind: "OLMConfig"},
		}
	})
	return nil
}

func ReconcileOLMOperatorServiceMonitor(sm *prometheusoperatorv1.ServiceMonitor, ownerRef config.OwnerRef, clusterID string, metricsSet metrics.MetricsSet) error {
	ownerRef.ApplyTo(sm)

	sm.Spec.Selector.MatchLabels = olmOperatorLabels()
	sm.Spec.NamespaceSelector = prometheusoperatorv1.NamespaceSelector{
		MatchNames: []string{sm.Namespace},
	}
	targetPort := intstr.FromString("metrics")
	sm.Spec.Endpoints = []prometheusoperatorv1.Endpoint{
		{
			TargetPort: &targetPort,
			Scheme:     "https",
			TLSConfig: &prometheusoperatorv1.TLSConfig{
				SafeTLSConfig: prometheusoperatorv1.SafeTLSConfig{
					ServerName: "olm-operator-metrics",
					Cert: prometheusoperatorv1.SecretOrConfigMap{
						Secret: &corev1.SecretKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: manifests.MetricsClientCertSecret(sm.Namespace).Name,
							},
							Key: "tls.crt",
						},
					},
					KeySecret: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{
							Name: manifests.MetricsClientCertSecret(sm.Namespace).Name,
						},
						Key: "tls.key",
					},
					CA: prometheusoperatorv1.SecretOrConfigMap{
						ConfigMap: &corev1.ConfigMapKeySelector{
							LocalObjectReference: corev1.LocalObjectReference{
								Name: manifests.RootCAConfigMap(sm.Namespace).Name,
							},
							Key: certs.CASignerCertMapKey,
						},
					},
				},
			},
			MetricRelabelConfigs: metrics.OLMRelabelConfigs(metricsSet),
		},
	}

	util.ApplyClusterIDLabel(&sm.Spec.Endpoints[0], clusterID)

	return nil
}
