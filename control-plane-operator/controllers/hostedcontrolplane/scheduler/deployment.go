package scheduler

import (
	"fmt"
	"path"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/pointer"

	configv1 "github.com/openshift/api/config/v1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
)

const (
	schedulerSecurePort = 10259
)

var (
	volumeMounts = util.PodVolumeMounts{
		schedulerContainerMain().Name: {
			schedulerVolumeConfig().Name:      "/etc/kubernetes/config",
			schedulerVolumeCertWorkDir().Name: "/var/run/kubernetes",
			schedulerVolumeKubeconfig().Name:  "/etc/kubernetes/kubeconfig",
		},
	}
	schedulerLabels = map[string]string{
		"app": "kube-scheduler",
	}
)

func ReconcileDeployment(deployment *appsv1.Deployment, ownerRef config.OwnerRef, config config.DeploymentConfig, image string, featureGates []string, policy configv1.ConfigMapNameReference, availabilityProberImage string, apiPort *int32) error {
	ownerRef.ApplyTo(deployment)

	// preserve existing resource requirements for main scheduler container
	mainContainer := util.FindContainer(schedulerContainerMain().Name, deployment.Spec.Template.Spec.Containers)
	if mainContainer != nil {
		config.SetContainerResourcesIfPresent(mainContainer)
	}

	maxSurge := intstr.FromInt(3)
	maxUnavailable := intstr.FromInt(1)
	deployment.Spec = appsv1.DeploymentSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: schedulerLabels,
		},
		Strategy: appsv1.DeploymentStrategy{
			Type: appsv1.RollingUpdateDeploymentStrategyType,
			RollingUpdate: &appsv1.RollingUpdateDeployment{
				MaxSurge:       &maxSurge,
				MaxUnavailable: &maxUnavailable,
			},
		},
		Template: corev1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Labels: schedulerLabels,
			},
			Spec: corev1.PodSpec{
				AutomountServiceAccountToken: pointer.BoolPtr(false),
				Containers: []corev1.Container{
					util.BuildContainer(schedulerContainerMain(), buildSchedulerContainerMain(image, deployment.Namespace, featureGates, policy)),
				},
				Volumes: []corev1.Volume{
					util.BuildVolume(schedulerVolumeConfig(), buildSchedulerVolumeConfig),
					util.BuildVolume(schedulerVolumeCertWorkDir(), buildSchedulerVolumeCertWorkDir),
					util.BuildVolume(schedulerVolumeKubeconfig(), buildSchedulerVolumeKubeconfig),
				},
			},
		},
	}
	config.ApplyTo(deployment)
	util.AvailabilityProber(kas.InClusterKASReadyURL(deployment.Namespace, apiPort), availabilityProberImage, &deployment.Spec.Template.Spec)
	return nil
}

func schedulerContainerMain() *corev1.Container {
	return &corev1.Container{
		Name: "kube-scheduler",
	}
}

func buildSchedulerContainerMain(image, namespace string, featureGates []string, policy configv1.ConfigMapNameReference) func(*corev1.Container) {
	return func(c *corev1.Container) {
		kubeConfigPath := path.Join(volumeMounts.Path(schedulerContainerMain().Name, schedulerVolumeKubeconfig().Name), kas.KubeconfigKey)
		configPath := path.Join(volumeMounts.Path(schedulerContainerMain().Name, schedulerVolumeConfig().Name), KubeSchedulerConfigKey)
		certWorkDir := volumeMounts.Path(schedulerContainerMain().Name, schedulerVolumeCertWorkDir().Name)
		c.Image = image
		c.Command = []string{
			"hyperkube",
			"kube-scheduler",
		}
		c.Args = []string{
			fmt.Sprintf("--config=%s", configPath),
			fmt.Sprintf("--cert-dir=%s", certWorkDir),
			fmt.Sprintf("--secure-port=%d", schedulerSecurePort),
			fmt.Sprintf("--authentication-kubeconfig=%s", kubeConfigPath),
			fmt.Sprintf("--authorization-kubeconfig=%s", kubeConfigPath),
			"-v=2",
		}
		for _, f := range featureGates {
			c.Args = append(c.Args, fmt.Sprintf("--feature-gates=%s", f))
		}
		if len(policy.Name) > 0 {
			c.Args = append(c.Args, fmt.Sprintf("--policy-config-map=%s", policy.Name))
			c.Args = append(c.Args, fmt.Sprintf("--policy-config-namespace=%s", namespace))
		}
		c.VolumeMounts = volumeMounts.ContainerMounts(c.Name)
	}
}

func schedulerVolumeConfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "scheduler-config",
	}
}

func buildSchedulerVolumeConfig(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	v.ConfigMap.Name = manifests.SchedulerConfig("").Name
}

func schedulerVolumeCertWorkDir() *corev1.Volume {
	return &corev1.Volume{
		Name: "cert-work",
	}
}

func buildSchedulerVolumeCertWorkDir(v *corev1.Volume) {
	v.EmptyDir = &corev1.EmptyDirVolumeSource{}
}

func schedulerVolumeKubeconfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "kubeconfig",
	}
}

func buildSchedulerVolumeKubeconfig(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: manifests.KASServiceKubeconfigSecret("").Name,
	}
}
