package routecm

import (
	"path"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"
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
)

var _ component.DeploymentReconciler = &RouteControllerManagerReconciler{}

type RouteControllerManagerReconciler struct {
}

func NewComponent() component.ControlPlaneComponent {
	return component.NewDeploymentComponent(&RouteControllerManagerReconciler{}).
		MultiZoneSpreadLabels(openShiftRouteControllerManagerLabels()).
		ResourcesReconcilers(
			component.NewReconcilerFor(&corev1.ConfigMap{}).
				WithName(ConfigMapName).
				WithReconcileFunction(ReconcileOpenShiftRouteControllerManagerConfig).
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

	maxSurge := intstr.FromInt(0)
	maxUnavailable := intstr.FromInt(1)
	deployment.Spec.Strategy.Type = appsv1.RollingUpdateDeploymentStrategyType
	deployment.Spec.Strategy.RollingUpdate = &appsv1.RollingUpdateDeployment{
		MaxSurge:       &maxSurge,
		MaxUnavailable: &maxUnavailable,
	}
	if deployment.Spec.Selector == nil {
		deployment.Spec.Selector = &metav1.LabelSelector{
			MatchLabels: openShiftRouteControllerManagerLabels(),
		}
	}
	deployment.Spec.Template.ObjectMeta.Labels = openShiftRouteControllerManagerLabels()
	deployment.Spec.Template.Spec.Containers = []corev1.Container{
		util.BuildContainer(routeOCMContainerMain(), buildRouteOCMContainerMain(image)),
	}

	return nil
}

var (
	volumes = component.Volumes{
		routeOCMVolumeConfig().Name: component.Volume{
			Source: component.ConfigMapVolumeSource(ConfigMapName),
			Mounts: map[string]string{
				routeOCMContainerMain().Name: "/etc/kubernetes/config",
			},
		},
		routeOCMVolumeServingCert().Name: component.Volume{
			Source: component.SecretVolumeSource(manifests.OpenShiftRouteControllerManagerCertSecret("").Name),
			Mounts: map[string]string{
				routeOCMContainerMain().Name: "/etc/kubernetes/certs",
			},
		},
		routeOCMVolumeKubeconfig().Name: component.Volume{
			Source: component.SecretVolumeSource(manifests.KASServiceKubeconfigSecret("").Name),
			Mounts: map[string]string{
				routeOCMContainerMain().Name: "/etc/kubernetes/secrets/svc-kubeconfig",
			},
		},
		common.VolumeTotalClientCA().Name: component.Volume{
			Source: component.ConfigMapVolumeSource(manifests.TotalClientCABundle("").Name),
			Mounts: map[string]string{
				routeOCMContainerMain().Name: "/etc/kubernetes/client-ca", // comes from the generic OCM config
			},
		},
	}
)

// Volumes implements controlplanecomponent.DeploymentReconciler.
func (r *RouteControllerManagerReconciler) Volumes(cpContext component.ControlPlaneContext) component.Volumes {
	return volumes
}

func openShiftRouteControllerManagerLabels() map[string]string {
	return map[string]string{
		"app":                         "openshift-route-controller-manager",
		hyperv1.ControlPlaneComponent: "openshift-route-controller-manager",
	}
}

func routeOCMContainerMain() *corev1.Container {
	return &corev1.Container{
		Name: "route-controller-manager",
	}
}

func buildRouteOCMContainerMain(image string) func(*corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = image
		c.Command = []string{"route-controller-manager"}
		c.Args = []string{
			"start",
			"--config",
			path.Join(volumes.Path(c.Name, routeOCMVolumeConfig().Name), configKey),
			"--kubeconfig",
			path.Join(volumes.Path(c.Name, routeOCMVolumeKubeconfig().Name), kas.KubeconfigKey),
			"--namespace=openshift-route-controller-manager",
		}
		c.Ports = []corev1.ContainerPort{
			{
				Name:          "https",
				ContainerPort: servingPort,
				Protocol:      corev1.ProtocolTCP,
			},
		}
		c.Env = []corev1.EnvVar{
			{
				Name: "POD_NAME",
				ValueFrom: &corev1.EnvVarSource{
					FieldRef: &corev1.ObjectFieldSelector{
						FieldPath: "metadata.name",
					},
				},
			},
			{
				Name:  "POD_NAMESPACE",
				Value: "openshift-route-controller-manager",
			},
		}
		c.Resources = corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("100Mi"),
				corev1.ResourceCPU:    resource.MustParse("100m"),
			},
		}
		c.LivenessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path:   "/healthz",
					Port:   intstr.FromInt(int(servingPort)),
					Scheme: corev1.URISchemeHTTPS,
				},
			},
			InitialDelaySeconds: 30,
			TimeoutSeconds:      5,
		}
		c.ReadinessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path:   "/healthz",
					Port:   intstr.FromInt(int(servingPort)),
					Scheme: corev1.URISchemeHTTPS,
				},
			},
			FailureThreshold: 10,
			TimeoutSeconds:   5,
		}
	}
}

func routeOCMVolumeConfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "config",
	}
}

func routeOCMVolumeKubeconfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "kubeconfig",
	}
}

func routeOCMVolumeServingCert() *corev1.Volume {
	return &corev1.Volume{
		Name: "serving-cert",
	}
}
