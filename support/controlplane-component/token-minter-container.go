package controlplanecomponent

import (
	"fmt"
	"path"
	"strings"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	tokenFileMountPath  = "/var/run/secrets/openshift/serviceaccount"
	kubeconfigMountPath = "/etc/kubernetes"
)

type TokenMinterContainerOptions struct {
	ServiceAccountName      string
	ServiceAccountNameSpace string

	// defaults to 'token-minter'
	ContainerName string
	// defaults to 'kubeconfig'
	KubeconfingVolumeName string
	// if true, will also add issuerURL as an audience in the serviceAccount token to allow access to the kube-apiserver.
	NeedsKubeApiToken bool
	//if true, token-minter container will exit after minting the token.
	OneShot bool
}

func (opts TokenMinterContainerOptions) injectTokenMinterContainer(cpContext ControlPlaneContext, podSpec *corev1.PodSpec) {
	// we only mint tokens for AWS web-identity access or for authentication with the kube-apiserver when opts.NeedsKubeApiToken is true.
	if cpContext.HCP.Spec.Platform.Type != hyperv1.AWSPlatform && !opts.NeedsKubeApiToken {
		return
	}

	if opts.ServiceAccountName == "" || opts.ServiceAccountNameSpace == "" {
		// programmer error.
		panic("ServiceAccountName and ServiceAccountNameSpace must be specified!")
	}

	tokenVolume := opts.buildVolume()
	podSpec.Volumes = append(podSpec.Volumes, tokenVolume)

	image := cpContext.ReleaseImageProvider.GetImage("token-minter")
	podSpec.Containers = append(podSpec.Containers, opts.buildContainer(cpContext.HCP, image, tokenVolume))

	// add volume mounts to main container
	podSpec.Containers[0].VolumeMounts = append(podSpec.Containers[0].VolumeMounts, corev1.VolumeMount{
		Name:      tokenVolume.Name,
		MountPath: tokenFileMountPath,
	})
	if opts.NeedsKubeApiToken {
		podSpec.Containers[0].VolumeMounts = append(podSpec.Containers[0].VolumeMounts, corev1.VolumeMount{
			Name:      tokenVolume.Name,
			MountPath: "/var/run/secrets/kubernetes.io/serviceaccount",
		})
	}
}

func (opts TokenMinterContainerOptions) buildContainer(hcp *hyperv1.HostedControlPlane, image string, tokenVolume corev1.Volume) corev1.Container {
	command := []string{"/usr/bin/control-plane-operator", "token-minter"}

	audiences := []string{"openshift"}
	if opts.NeedsKubeApiToken {
		audiences = append(audiences, hcp.Spec.IssuerURL)
	}
	args := []string{
		fmt.Sprintf("--token-audience=%s", strings.Join(audiences, ",")),
		fmt.Sprintf("--service-account-namespace=%s", opts.ServiceAccountNameSpace),
		fmt.Sprintf("--service-account-name=%s", opts.ServiceAccountName),
		fmt.Sprintf("--token-file=%s", path.Join(tokenFileMountPath, "token")),
		fmt.Sprintf("--kubeconfig=%s", path.Join(kubeconfigMountPath, util.KubeconfigKey)),
	}
	if opts.OneShot {
		args = append(args, "--oneshot")
	}

	kubeconfingVolumeName := opts.KubeconfingVolumeName
	if kubeconfingVolumeName == "" {
		kubeconfingVolumeName = "kubeconfig"
	}

	containerName := opts.ContainerName
	if containerName == "" {
		containerName = "token-minter"
	}

	container := corev1.Container{
		Name:            containerName,
		Image:           image,
		Command:         command,
		Args:            args,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10m"),
				corev1.ResourceMemory: resource.MustParse("10Mi"),
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      kubeconfingVolumeName,
				MountPath: kubeconfigMountPath,
			},
			{
				Name:      tokenVolume.Name,
				MountPath: tokenFileMountPath,
			},
		},
	}

	return container
}

func (opts TokenMinterContainerOptions) buildVolume() corev1.Volume {
	return corev1.Volume{
		Name: "serviceaccount-token",
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumMemory},
		},
	}
}
