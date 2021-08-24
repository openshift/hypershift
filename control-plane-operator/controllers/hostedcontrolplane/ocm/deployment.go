package ocm

import (
	"path"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/config"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/util"
)

var (
	volumeMounts = util.PodVolumeMounts{
		ocmContainerMain().Name: {
			ocmVolumeConfig().Name:      "/etc/kubernetes/config",
			ocmVolumeServingCert().Name: "/etc/kubernetes/certs",
			ocmVolumeKubeconfig().Name:  "/etc/kubernetes/secrets/svc-kubeconfig",
		},
	}
	openShiftControllerManagerLabels = map[string]string{
		"app": "openshift-controller-manager",
	}
)

func ReconcileDeployment(deployment *appsv1.Deployment, ownerRef config.OwnerRef, image string, deploymentConfig config.DeploymentConfig) error {
	maxSurge := intstr.FromInt(1)
	maxUnavailable := intstr.FromInt(0)
	deployment.Spec.Strategy.Type = appsv1.RollingUpdateDeploymentStrategyType
	deployment.Spec.Strategy.RollingUpdate = &appsv1.RollingUpdateDeployment{
		MaxSurge:       &maxSurge,
		MaxUnavailable: &maxUnavailable,
	}
	deployment.Spec.Selector = &metav1.LabelSelector{
		MatchLabels: openShiftControllerManagerLabels,
	}
	deployment.Spec.Template.ObjectMeta.Labels = openShiftControllerManagerLabels
	deployment.Spec.Template.Spec.AutomountServiceAccountToken = pointer.BoolPtr(false)
	deployment.Spec.Template.Spec.Containers = []corev1.Container{
		util.BuildContainer(ocmContainerMain(), buildOCMContainerMain(image)),
	}
	deployment.Spec.Template.Spec.Volumes = []corev1.Volume{
		util.BuildVolume(ocmVolumeConfig(), buildOCMVolumeConfig),
		util.BuildVolume(ocmVolumeServingCert(), buildOCMVolumeServingCert),
		util.BuildVolume(ocmVolumeKubeconfig(), buildOCMVolumeKubeconfig),
	}
	deploymentConfig.ApplyTo(deployment)
	return nil
}

func ocmContainerMain() *corev1.Container {
	return &corev1.Container{
		Name: "openshift-controller-manager",
	}
}

func buildOCMContainerMain(image string) func(*corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = image
		c.Command = []string{"openshift-controller-manager"}
		c.Args = []string{
			"start",
			"--config",
			path.Join(volumeMounts.Path(c.Name, ocmVolumeConfig().Name), configKey),
		}
		c.VolumeMounts = volumeMounts.ContainerMounts(c.Name)
	}
}

func ocmVolumeConfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "config",
	}
}

func buildOCMVolumeConfig(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	v.ConfigMap.Name = manifests.OpenShiftControllerManagerConfig("").Name
}

func ocmVolumeKubeconfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "kubeconfig",
	}
}

func buildOCMVolumeKubeconfig(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{}
	v.Secret.SecretName = manifests.KASServiceKubeconfigSecret("").Name
}

func ocmVolumeServingCert() *corev1.Volume {
	return &corev1.Volume{
		Name: "serving-cert",
	}
}

func buildOCMVolumeServingCert(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{}
	v.Secret.SecretName = manifests.OpenShiftControllerManagerCertSecret("").Name
}
