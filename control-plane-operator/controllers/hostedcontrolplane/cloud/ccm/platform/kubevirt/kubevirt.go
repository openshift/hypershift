package kubevirt

import (
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/pointer"
)

var (
	// TODO nargaman need to add to images
	image = "quay.io/nargaman/kubevirt-cloud-controller-manager:v0.1.0"
)

type Kubevirt struct {
	hcp *hyperv1.HostedControlPlane
}

func NewKubevirtPlatform(hcp *hyperv1.HostedControlPlane) *Kubevirt {
	return &Kubevirt{
		hcp: hcp,
	}
}

func (k *Kubevirt) GetContainerImage() string {
	return image
}

func (k *Kubevirt) GetContainerCommand() []string {
	return []string{"/bin/kubevirt-cloud-controller-manager"}
}

func (k *Kubevirt) GetContainerArgs() []string {
	return []string{
		"--cloud-provider=kubevirt",
		"--cloud-config=/etc/cloud/cloud-config",
		// "--use-service-account-credentials=true",
		"--kubeconfig=/etc/kubernetes/kubeconfig/kubeconfig",
		"--cluster-name", k.hcp.Name,
	}
}

func (k *Kubevirt) GetPodVolumeMounts() util.PodVolumeMounts {
	return util.PodVolumeMounts{
		manifests.CCMContainer().Name: util.ContainerVolumeMounts{
			ccmVolumeKubeconfig().Name:      "/etc/kubernetes/kubeconfig",
			ccmVolumeCombinedCA().Name:      "/etc/kubernetes/combined-ca",
			ccmVolumeClusterSignerCA().Name: "/etc/kubernetes/cluster-signer-ca",
			ccmCloudConfig().Name:           "/etc/cloud",
		},
	}
}

func (k *Kubevirt) AddPlatfomVolumes(deployment *appsv1.Deployment) {

	deployment.Spec.Template.Spec.Volumes = append(
		deployment.Spec.Template.Spec.Volumes,
		util.BuildVolume(ccmVolumeKubeconfig(), buildCCMVolumeKubeconfig),
	)
	deployment.Spec.Template.Spec.Volumes = append(
		deployment.Spec.Template.Spec.Volumes,
		util.BuildVolume(ccmVolumeCombinedCA(), buildCCMVolumeCombinedCA),
	)
	deployment.Spec.Template.Spec.Volumes = append(
		deployment.Spec.Template.Spec.Volumes,
		util.BuildVolume(ccmVolumeClusterSignerCA(), buildCCMClusterSignerCA),
	)
	deployment.Spec.Template.Spec.Volumes = append(
		deployment.Spec.Template.Spec.Volumes,
		util.BuildVolume(ccmCloudConfig(), buildCCMCloudConfig),
	)
}

func ccmVolumeKubeconfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "kubeconfig",
	}
}

func ccmVolumeCombinedCA() *corev1.Volume {
	return &corev1.Volume{
		Name: "combined-ca",
	}
}

func ccmVolumeClusterSignerCA() *corev1.Volume {
	return &corev1.Volume{
		Name: "cluster-signer-ca",
	}
}

func ccmCloudConfig() *corev1.Volume {
	return &corev1.Volume{
		Name: "cloud-config",
	}
}

func buildCCMVolumeKubeconfig(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: manifests.KASServiceKubeconfigSecret("").Name,
	}
}

func buildCCMVolumeCombinedCA(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	v.ConfigMap.DefaultMode = pointer.Int32Ptr(420)
	v.ConfigMap.Name = manifests.CombinedCAConfigMap("").Name
}

func buildCCMClusterSignerCA(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: manifests.ClusterSignerCASecret("").Name,
	}
}

func buildCCMCloudConfig(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{
		LocalObjectReference: corev1.LocalObjectReference{
			Name: manifests.CCMConfigMap("").Name,
		},
	}
}
