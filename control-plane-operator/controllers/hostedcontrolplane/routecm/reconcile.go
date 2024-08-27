package routecm

import (
	"path"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	component "github.com/openshift/hypershift/support/controlplane-component"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
)

const (
	ComponentName = "openshift-route-controller-manager"

	ConfigMapName = "openshift-route-controller-manager-config"

	configHashAnnotation       = "openshift-route-controller-manager.hypershift.openshift.io/config-hash"
	servingPort          int32 = 8443

	configVolumeName      = "config"
	kubeconfigVolumeName  = "kubeconfig"
	servingCertVolumeName = "serving-cert"
)

var _ component.DeploymentReconciler = &RouteControllerManagerReconciler{}

type RouteControllerManagerReconciler struct {
}

func NewComponent() component.ControlPlaneComponent {
	reconciler := &RouteControllerManagerReconciler{}
	return component.NewDeploymentComponent(reconciler).
		MultiZoneSpreadLabels(openShiftRouteControllerManagerLabels()).
		ResourcesReconcilers(
			component.NewReconcilerFor(&corev1.ConfigMap{}).
				WithName(ConfigMapName).
				WithReconcileFunction(reconciler.reconcileConfigMap).
				Build(),
			component.NewReconcilerFor(&corev1.Service{}).
				WithName(ComponentName).
				WithReconcileFunction(ReconcileService).
				WithPredicate(component.DisableIfAnnotationExist(hyperv1.DisableMonitoringServices)).
				Build(),
			component.NewReconcilerFor(&prometheusoperatorv1.ServiceMonitor{}).
				WithName(ComponentName).
				WithReconcileFunction(ReconcileServiceMonitor).
				WithPredicate(component.DisableIfAnnotationExist(hyperv1.DisableMonitoringServices)).
				Build(),
		).
		WatchResources(&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: ConfigMapName,
			},
		}).
		Build()
}

// Name implements controlplanecomponent.DeploymentReconciler.
func (r *RouteControllerManagerReconciler) Name() string {
	return ComponentName
}

// ReconcileDeployment implements controlplanecomponent.DeploymentReconciler.
func (r *RouteControllerManagerReconciler) ReconcileDeployment(cpContext component.ControlPlaneContext, deployment *appsv1.Deployment) error {
	image := cpContext.ReleaseImageProvider.GetImage("route-controller-manager")

	if deployment.Spec.Selector == nil {
		deployment.Spec.Selector = &metav1.LabelSelector{
			MatchLabels: openShiftRouteControllerManagerLabels(),
		}
	}
	deployment.Spec.Template.ObjectMeta.Labels = openShiftRouteControllerManagerLabels()
	deployment.Spec.Template.Spec.Containers = []corev1.Container{
		buildContainer(image, r.Volumes(cpContext)),
	}

	return nil
}

// Volumes implements controlplanecomponent.DeploymentReconciler.
func (r *RouteControllerManagerReconciler) Volumes(cpContext component.ControlPlaneContext) component.Volumes {
	return component.Volumes{
		configVolumeName: component.Volume{
			Source: component.ConfigMapVolumeSource(ConfigMapName),
			Mounts: map[string]string{
				ComponentName: "/etc/kubernetes/config",
			},
		},
		servingCertVolumeName: component.Volume{
			Source: component.SecretVolumeSource(manifests.OpenShiftRouteControllerManagerCertSecret("").Name),
			Mounts: map[string]string{
				ComponentName: "/etc/kubernetes/certs",
			},
		},
		kubeconfigVolumeName: component.Volume{
			Source: component.SecretVolumeSource(manifests.KASServiceKubeconfigSecret("").Name),
			Mounts: map[string]string{
				ComponentName: "/etc/kubernetes/secrets/svc-kubeconfig",
			},
		},
		common.VolumeTotalClientCA().Name: component.Volume{
			Source: component.ConfigMapVolumeSource(manifests.TotalClientCABundle("").Name),
			Mounts: map[string]string{
				ComponentName: "/etc/kubernetes/client-ca", // comes from the generic OCM config
			},
		},
	}
}

func openShiftRouteControllerManagerLabels() map[string]string {
	return map[string]string{
		"app":                         "openshift-route-controller-manager",
		hyperv1.ControlPlaneComponent: "openshift-route-controller-manager",
	}
}

func buildContainer(image string, volumes component.Volumes) corev1.Container {
	return component.NewContainer(ComponentName).
		Image(image).
		Command("route-controller-manager").
		WithArgs("start").
		WithArgs("--config", path.Join(volumes.Path(ComponentName, configVolumeName), configKey)).
		WithArgs("--kubeconfig", path.Join(volumes.Path(ComponentName, kubeconfigVolumeName), kas.KubeconfigKey)).
		WithArgs("--namespace=openshift-route-controller-manager").
		WithPort(corev1.ContainerPort{
			Name:          "https",
			ContainerPort: servingPort,
			Protocol:      corev1.ProtocolTCP,
		}).
		WithStringEnv("POD_NAMESPACE", "openshift-route-controller-manager").
		WithEnv("POD_NAME", &corev1.EnvVarSource{
			FieldRef: &corev1.ObjectFieldSelector{
				FieldPath: "metadata.name",
			},
		}).
		WithMemoryResourcesRequest(resource.MustParse("100Mi")).
		WithCPUResourcesRequest(resource.MustParse("100m")).
		WithHTTPLivnessProbe(&corev1.HTTPGetAction{
			Path:   "/healthz",
			Port:   intstr.FromInt(int(servingPort)),
			Scheme: corev1.URISchemeHTTPS,
		}).
		WithHTTPReadinessProbe(&corev1.HTTPGetAction{
			Path:   "/healthz",
			Port:   intstr.FromInt(int(servingPort)),
			Scheme: corev1.URISchemeHTTPS,
		}).
		Build()
}
