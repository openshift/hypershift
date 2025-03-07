package controlplanecomponent

import (
	"fmt"
	"path"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	cloudTokenFileMountPath   = "/var/run/secrets/openshift/serviceaccount"
	kubeAPITokenFileMountPath = "/var/run/secrets/kubernetes.io/serviceaccount"
)

type TokenType string

const (
	CloudToken         TokenType = "cloud"
	KubeAPIServerToken TokenType = "apiserver"

	// CloudAndAPIServerToken will inject 2 token-minter containers, one using minting a token for cloud and the other minting a token for kube-apiserver access.
	CloudAndAPIServerToken TokenType = "cloud-and-apiserver"
)

// TokenMinterContainerOptions defines the options for token-minter sidecar container which mints ServiceAccount tokens in the tenant cluster for the given named service account,
// and then make it available for the main container with a volume mount.
type TokenMinterContainerOptions struct {
	// TokenType defines the token purpose, either to grant cloud access, kube-apiserver access to both.
	TokenType TokenType
	// ServiceAccountName is the name of the service account for which to mint a token.
	ServiceAccountName string
	// ServiceAccountNameSpace is the namespace of the service account for which to mint a token.
	ServiceAccountNameSpace string

	// KubeconfingVolumeName is the volume name which contains the kubeconfig to use mint the token in the target cluster.
	// defaults to 'kubeconfig'
	KubeconfingVolumeName string
	// OneShot, if true, will cause the token-minter container to exit after minting the token.
	OneShot bool
}

func (opts TokenMinterContainerOptions) injectTokenMinterContainer(cpContext ControlPlaneContext, podSpec *corev1.PodSpec) {
	if opts.TokenType == "" || opts.ServiceAccountName == "" || opts.ServiceAccountNameSpace == "" {
		// programmer error.
		panic("tokenTarget, ServiceAccountName and ServiceAccountNameSpace must be specified!")
	}
	image := cpContext.ReleaseImageProvider.GetImage("token-minter")

	// we only mint cloud tokens for AWS.
	if (opts.TokenType == CloudToken || opts.TokenType == CloudAndAPIServerToken) &&
		cpContext.HCP.Spec.Platform.Type == hyperv1.AWSPlatform {
		tokenVolume := opts.buildVolume(string(CloudToken))
		podSpec.Volumes = append(podSpec.Volumes, tokenVolume)

		podSpec.Containers = append(podSpec.Containers, opts.buildContainer(cpContext.HCP, CloudToken, image, tokenVolume))

		podSpec.Containers[0].VolumeMounts = append(podSpec.Containers[0].VolumeMounts, corev1.VolumeMount{
			Name:      tokenVolume.Name,
			MountPath: cloudTokenFileMountPath,
		})
	}

	if opts.TokenType == KubeAPIServerToken || opts.TokenType == CloudAndAPIServerToken {
		tokenVolume := opts.buildVolume(string(KubeAPIServerToken))
		podSpec.Volumes = append(podSpec.Volumes, tokenVolume)

		podSpec.Containers = append(podSpec.Containers, opts.buildContainer(cpContext.HCP, KubeAPIServerToken, image, tokenVolume))

		podSpec.Containers[0].VolumeMounts = append(podSpec.Containers[0].VolumeMounts, corev1.VolumeMount{
			Name:      tokenVolume.Name,
			MountPath: kubeAPITokenFileMountPath,
		})
	}
}

func (opts TokenMinterContainerOptions) buildContainer(hcp *hyperv1.HostedControlPlane, tokenType TokenType, image string, tokenVolume corev1.Volume) corev1.Container {
	tokenFileMountPath := "/var/run/secrets/openshift/serviceaccount"
	kubeconfigMountPath := "/etc/kubernetes"

	var audience string
	if tokenType == CloudToken {
		audience = "openshift"
	} else if tokenType == KubeAPIServerToken {
		audience = hcp.Spec.IssuerURL
	}
	args := []string{
		fmt.Sprintf("--token-audience=%s", audience),
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

	container := corev1.Container{
		Name:            fmt.Sprintf("%s-token-minter", tokenType),
		Image:           image,
		Command:         []string{"/usr/bin/control-plane-operator", "token-minter"},
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

func (opts TokenMinterContainerOptions) buildVolume(namePrefix string) corev1.Volume {
	return corev1.Volume{
		Name: fmt.Sprintf("%s-token", namePrefix),
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumMemory},
		},
	}
}
