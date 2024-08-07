package routecm

import (
	"fmt"
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
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

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
	return &component.ControlPlaneWorkload{
		DeploymentReconciler: &RouteControllerManagerReconciler{},
		ResourcesReconcilers: []component.GenericReconciler{
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
		},

		MultiZoneSpreadLabels: openShiftRouteControllerManagerLabels(),
	}
}

// Name implements controlplanecomponent.DeploymentReconciler.
func (r *RouteControllerManagerReconciler) Name() string {
	return ComponentName
}

// ReconcileDeployment implements controlplanecomponent.DeploymentReconciler.
func (r *RouteControllerManagerReconciler) ReconcileDeployment(cpContext component.ControlPlaneContext, deployment *appsv1.Deployment) error {
	image := cpContext.ReleaseImageProvider.GetImage("route-controller-manager")

	config := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ConfigMapName,
			Namespace: cpContext.HCP.Namespace,
		},
	}
	if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(config), config); err != nil {
		return fmt.Errorf("failed to get openshift controller manager config: %w", err)
	}

	configBytes, ok := config.Data[configKey]
	if !ok {
		return fmt.Errorf("openshift controller manager configuration is not expected to be empty")
	}
	configHash := util.ComputeHash(configBytes)

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
	deployment.Spec.Template.ObjectMeta.Annotations = map[string]string{
		configHashAnnotation: configHash,
	}
	deployment.Spec.Template.Spec.AutomountServiceAccountToken = ptr.To(false)
	deployment.Spec.Template.Spec.Containers = []corev1.Container{
		util.BuildContainer(routeOCMContainerMain(), buildRouteOCMContainerMain(image)),
	}
	deployment.Spec.Template.Spec.Volumes = []corev1.Volume{
		util.BuildVolume(routeOCMVolumeConfig(), buildRouteOCMVolumeConfig),
		util.BuildVolume(routeOCMVolumeServingCert(), buildRouteOCMVolumeServingCert),
		util.BuildVolume(routeOCMVolumeKubeconfig(), buildRouteOCMVolumeKubeconfig),
		util.BuildVolume(common.VolumeTotalClientCA(), common.BuildVolumeTotalClientCA),
	}

	return nil
}

var (
	volumeMounts = util.PodVolumeMounts{
		routeOCMContainerMain().Name: {
			routeOCMVolumeConfig().Name:       "/etc/kubernetes/config",
			routeOCMVolumeServingCert().Name:  "/etc/kubernetes/certs",
			routeOCMVolumeKubeconfig().Name:   "/etc/kubernetes/secrets/svc-kubeconfig",
			common.VolumeTotalClientCA().Name: "/etc/kubernetes/client-ca", // comes from the generic OCM config
		},
	}
)

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
			path.Join(volumeMounts.Path(c.Name, routeOCMVolumeConfig().Name), configKey),
			"--kubeconfig",
			path.Join(volumeMounts.Path(c.Name, routeOCMVolumeKubeconfig().Name), kas.KubeconfigKey),
			"--namespace=openshift-route-controller-manager",
		}
		c.VolumeMounts = volumeMounts.ContainerMounts(c.Name)
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

func buildRouteOCMVolumeConfig(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	v.ConfigMap.Name = ConfigMapName
}

func routeOCMVolumeKubeconfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "kubeconfig",
	}
}

func buildRouteOCMVolumeKubeconfig(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{}
	v.Secret.SecretName = manifests.KASServiceKubeconfigSecret("").Name
	v.Secret.DefaultMode = ptr.To[int32](0640)
}

func routeOCMVolumeServingCert() *corev1.Volume {
	return &corev1.Volume{
		Name: "serving-cert",
	}
}

func buildRouteOCMVolumeServingCert(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{}
	v.Secret.SecretName = manifests.OpenShiftRouteControllerManagerCertSecret("").Name
	v.Secret.DefaultMode = ptr.To[int32](0640)
}
