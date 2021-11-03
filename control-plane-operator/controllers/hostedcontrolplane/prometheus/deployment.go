package prometheus

import (
	"path"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/util"
)

var (
	volumeMounts = util.PodVolumeMounts{
		prometheusContainerMain().Name: {
			prometheusVolumeWork().Name:      "/var/run/prometheus",
			prometheusVolumeConfig().Name:    "/etc/prometheus",
			prometheusVolumeRootCA().Name:    "/etc/kubernetes/root-ca",
			prometheusVolumeServiceCA().Name: "/etc/kubernetes/service-ca",
			util.TokenMinterTokenVolume:      "/etc/kubernetes/service",
		},
	}
	prometheusLabels = map[string]string{
		"app":                         "prometheus",
		hyperv1.ControlPlaneComponent: "prometheus",
	}
)

func ReconcileDeployment(deployment *appsv1.Deployment, ownerRef config.OwnerRef, image, tokenMinterImage, availabilityProberImage, tokenAudience string, apiPort *int32, deploymentConfig config.DeploymentConfig) error {
	ownerRef.ApplyTo(deployment)
	deployment.Spec.Selector = &metav1.LabelSelector{
		MatchLabels: prometheusLabels,
	}
	deployment.Spec.Strategy.Type = appsv1.RollingUpdateDeploymentStrategyType
	maxSurge := intstr.FromInt(3)
	maxUnavailable := intstr.FromInt(1)
	deployment.Spec.Strategy.RollingUpdate = &appsv1.RollingUpdateDeployment{
		MaxSurge:       &maxSurge,
		MaxUnavailable: &maxUnavailable,
	}
	if deployment.Spec.Template.ObjectMeta.Labels == nil {
		deployment.Spec.Template.ObjectMeta.Labels = map[string]string{}
	}
	for k, v := range prometheusLabels {
		deployment.Spec.Template.ObjectMeta.Labels[k] = v
	}
	if deployment.Spec.Template.ObjectMeta.Annotations == nil {
		deployment.Spec.Template.ObjectMeta.Annotations = map[string]string{}
	}
	deployment.Spec.Template.Spec = corev1.PodSpec{
		Containers: []corev1.Container{
			util.BuildContainer(prometheusContainerMain(), buildPrometheusContainerMain(image)),
		},
		Volumes: []corev1.Volume{
			util.BuildVolume(prometheusVolumeWork(), buildPrometheusVolumeWork),
			util.BuildVolume(prometheusVolumeConfig(), buildPrometheusVolumeConfig),
			util.BuildVolume(prometheusVolumeKubeconfig(), buildPrometheusVolumeKubeconfig),
			util.BuildVolume(prometheusVolumeRootCA(), buildPrometheusVolumeRootCA),
			util.BuildVolume(prometheusVolumeServiceCA(), buildPrometheusVolumeServiceCA),
		},
		ServiceAccountName: manifests.PrometheusServiceAccount(deployment.Namespace).Name,
	}
	deploymentConfig.ApplyTo(deployment)
	util.AvailabilityProber(kas.InClusterKASReadyURL(deployment.Namespace, apiPort), availabilityProberImage, &deployment.Spec.Template.Spec)
	util.TokenMinterInit(tokenMinterImage, "default", "metrics-collector", tokenAudience, prometheusVolumeKubeconfig().Name, kas.KubeconfigKey, &deployment.Spec.Template.Spec)
	return nil
}

func prometheusContainerMain() *corev1.Container {
	return &corev1.Container{
		Name: "prometheus",
	}
}

func buildPrometheusContainerMain(image string) func(*corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = image
		c.Args = []string{
			"--config.file",
			path.Join(volumeMounts.Path(c.Name, prometheusVolumeConfig().Name), prometheusConfigFileName),
			"--log.level=debug",
			"--storage.tsdb.retention.time=20m",
		}
		c.VolumeMounts = volumeMounts.ContainerMounts(c.Name)
	}
}

func prometheusVolumeWork() *corev1.Volume {
	return &corev1.Volume{
		Name: "work",
	}
}

func buildPrometheusVolumeWork(v *corev1.Volume) {
	v.EmptyDir = &corev1.EmptyDirVolumeSource{}
}

func prometheusVolumeKubeconfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "kubeconfig",
	}
}

func buildPrometheusVolumeKubeconfig(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: manifests.KASServiceKubeconfigSecret("").Name,
	}
}

func prometheusVolumeConfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "config",
	}
}

func buildPrometheusVolumeConfig(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	v.ConfigMap.Name = manifests.PrometheusConfiguration("").Name
}

func prometheusVolumeRootCA() *corev1.Volume {
	return &corev1.Volume{
		Name: "root-ca",
	}
}

func buildPrometheusVolumeRootCA(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{}
	v.Secret.SecretName = manifests.RootCASecret("").Name
}

func prometheusVolumeServiceCA() *corev1.Volume {
	return &corev1.Volume{
		Name: "service-ca",
	}
}

func buildPrometheusVolumeServiceCA(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	v.ConfigMap.Name = manifests.ServiceServingCA("").Name
}
