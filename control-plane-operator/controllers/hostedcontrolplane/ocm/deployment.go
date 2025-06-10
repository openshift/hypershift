package ocm

import (
	"fmt"
	"path"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/common"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

const (
	configHashAnnotation = "openshift-controller-manager.hypershift.openshift.io/config-hash"

	servingPort int32 = 8443
)

var (
	volumeMounts = util.PodVolumeMounts{
		ocmContainerMain().Name: {
			ocmVolumeConfig().Name:            "/etc/kubernetes/config",
			ocmVolumeServingCert().Name:       "/etc/kubernetes/certs",
			ocmVolumeKubeconfig().Name:        "/etc/kubernetes/secrets/svc-kubeconfig",
			common.VolumeTotalClientCA().Name: "/etc/kubernetes/client-ca",
		},
	}
)

func openShiftControllerManagerLabels() map[string]string {
	return map[string]string{
		"app":                              "openshift-controller-manager",
		hyperv1.ControlPlaneComponentLabel: "openshift-controller-manager",
	}
}

func ReconcileDeployment(deployment *appsv1.Deployment, ownerRef config.OwnerRef, image string, config *corev1.ConfigMap, deploymentConfig config.DeploymentConfig) error {
	configBytes, ok := config.Data[ConfigKey]
	if !ok {
		return fmt.Errorf("openshift apiserver configuration is not expected to be empty")
	}
	configHash := util.ComputeHash(configBytes)

	// preserve existing resource requirements for main OCM container
	mainContainer := util.FindContainer(ocmContainerMain().Name, deployment.Spec.Template.Spec.Containers)
	if mainContainer != nil {
		deploymentConfig.SetContainerResourcesIfPresent(mainContainer)
	}

	maxSurge := intstr.FromInt(1)
	maxUnavailable := intstr.FromInt(0)
	deployment.Spec.Strategy.Type = appsv1.RollingUpdateDeploymentStrategyType
	deployment.Spec.Strategy.RollingUpdate = &appsv1.RollingUpdateDeployment{
		MaxSurge:       &maxSurge,
		MaxUnavailable: &maxUnavailable,
	}
	if deployment.Spec.Selector == nil {
		deployment.Spec.Selector = &metav1.LabelSelector{
			MatchLabels: openShiftControllerManagerLabels(),
		}
	}
	deployment.Spec.Template.ObjectMeta.Labels = openShiftControllerManagerLabels()
	deployment.Spec.Template.ObjectMeta.Annotations = map[string]string{
		configHashAnnotation: configHash,
	}
	deployment.Spec.Template.Spec.AutomountServiceAccountToken = ptr.To(false)
	deployment.Spec.Template.Spec.Containers = []corev1.Container{
		util.BuildContainer(ocmContainerMain(), buildOCMContainerMain(image)),
	}
	deployment.Spec.Template.Spec.Volumes = []corev1.Volume{
		util.BuildVolume(ocmVolumeConfig(), buildOCMVolumeConfig),
		util.BuildVolume(ocmVolumeServingCert(), buildOCMVolumeServingCert),
		util.BuildVolume(ocmVolumeKubeconfig(), buildOCMVolumeKubeconfig),
		util.BuildVolume(common.VolumeTotalClientCA(), common.BuildVolumeTotalClientCA),
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
		c.TerminationMessagePolicy = corev1.TerminationMessageFallbackToLogsOnError
		c.Command = []string{"openshift-controller-manager"}
		c.Args = []string{
			"start",
			"--config",
			path.Join(volumeMounts.Path(c.Name, ocmVolumeConfig().Name), ConfigKey),
		}
		c.VolumeMounts = volumeMounts.ContainerMounts(c.Name)
		c.Ports = []corev1.ContainerPort{
			{
				Name:          "https",
				ContainerPort: servingPort,
				Protocol:      corev1.ProtocolTCP,
			},
		}
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
	v.Secret.DefaultMode = ptr.To[int32](0640)
}

func ocmVolumeServingCert() *corev1.Volume {
	return &corev1.Volume{
		Name: "serving-cert",
	}
}

func buildOCMVolumeServingCert(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{}
	v.Secret.SecretName = manifests.OpenShiftControllerManagerCertSecret("").Name
	v.Secret.DefaultMode = ptr.To[int32](0640)
}
