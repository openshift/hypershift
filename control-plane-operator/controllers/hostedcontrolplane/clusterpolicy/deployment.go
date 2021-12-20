package clusterpolicy

import (
	"path"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
)

var (
	volumeMounts = util.PodVolumeMounts{
		cpcContainerMain().Name: {
			cpcVolumeConfig().Name:      "/etc/kubernetes/config",
			cpcVolumeServingCert().Name: "/etc/kubernetes/certs",
			cpcVolumeKubeconfig().Name:  "/etc/kubernetes/secrets/svc-kubeconfig",
		},
	}
	clusterPolicyControllerLabels = map[string]string{
		"app":                         "cluster-policy-controller",
		hyperv1.ControlPlaneComponent: "cluster-policy-controller",
	}
)

func ReconcileDeployment(deployment *appsv1.Deployment, ownerRef config.OwnerRef, image string, deploymentConfig config.DeploymentConfig, availabilityProberImage string, apiServerPort *int32) error {
	maxSurge := intstr.FromInt(1)
	maxUnavailable := intstr.FromInt(0)
	deployment.Spec.Strategy.Type = appsv1.RollingUpdateDeploymentStrategyType
	deployment.Spec.Strategy.RollingUpdate = &appsv1.RollingUpdateDeployment{
		MaxSurge:       &maxSurge,
		MaxUnavailable: &maxUnavailable,
	}
	deployment.Spec.Selector = &metav1.LabelSelector{
		MatchLabels: clusterPolicyControllerLabels,
	}
	deployment.Spec.Template.ObjectMeta.Labels = clusterPolicyControllerLabels
	deployment.Spec.Template.Spec.Containers = []corev1.Container{
		util.BuildContainer(cpcContainerMain(), buildOCMContainerMain(image)),
	}
	deployment.Spec.Template.Spec.Volumes = []corev1.Volume{
		util.BuildVolume(cpcVolumeConfig(), buildCPCVolumeConfig),
		util.BuildVolume(cpcVolumeServingCert(), buildCPCVolumeServingCert),
		util.BuildVolume(cpcVolumeKubeconfig(), buildCPCVolumeKubeconfig),
	}
	deploymentConfig.ApplyTo(deployment)

	util.AvailabilityProber(kas.InClusterKASReadyURL(deployment.Namespace, apiServerPort), availabilityProberImage, &deployment.Spec.Template.Spec)
	return nil
}

func cpcContainerMain() *corev1.Container {
	return &corev1.Container{
		Name: "cluster-policy-controller",
	}
}

func buildOCMContainerMain(image string) func(*corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = image
		c.Command = []string{"cluster-policy-controller"}
		c.Args = []string{
			"start",
			"--config",
			path.Join(volumeMounts.Path(c.Name, cpcVolumeConfig().Name), configKey),
			"--kubeconfig",
			path.Join(volumeMounts.Path(c.Name, cpcVolumeKubeconfig().Name), kas.KubeconfigKey),
		}
		c.VolumeMounts = volumeMounts.ContainerMounts(c.Name)
		c.Env = []corev1.EnvVar{
			{
				// let policy-controller create events in the openshift-kube-controller-manager namespace instead of the default namespace.
				Name:  "POD_NAMESPACE",
				Value: "openshift-kube-controller-manager",
			},
		}
	}
}

func cpcVolumeConfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "config",
	}
}

func buildCPCVolumeConfig(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	v.ConfigMap.Name = manifests.ClusterPolicyControllerConfig("").Name
}

func cpcVolumeKubeconfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "kubeconfig",
	}
}

func buildCPCVolumeKubeconfig(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{}
	v.Secret.SecretName = manifests.KASServiceKubeconfigSecret("").Name
}

func cpcVolumeServingCert() *corev1.Volume {
	return &corev1.Volume{
		Name: "serving-cert",
	}
}

func buildCPCVolumeServingCert(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{}
	v.Secret.SecretName = manifests.ClusterPolicyControllerCertSecret("").Name
}
