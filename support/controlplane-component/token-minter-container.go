package controlplanecomponent

import (
	"fmt"
	"path"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/azureutil"
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

	// KubeconfingVolumeName is the volume name which contains the kubeconfig used to mint the token in the target cluster.
	// defaults to 'kubeconfig'
	KubeconfingVolumeName string

	// KubeconfigSecretName is the name of the the kubeconfig secret used to mint the token in the target cluster.
	KubeconfigSecretName string

	// OneShot, if true, will cause the token-minter container to exit after minting the token.
	OneShot bool
}

func (opts TokenMinterContainerOptions) injectTokenMinterContainer(cpContext ControlPlaneContext, podSpec *corev1.PodSpec) {
	if opts.TokenType == "" || opts.ServiceAccountName == "" || opts.ServiceAccountNameSpace == "" {
		// programmer error.
		panic("tokenTarget, ServiceAccountName and ServiceAccountNameSpace must be specified!")
	}
	image := cpContext.ReleaseImageProvider.GetImage("token-minter")

	// We mint cloud tokens for AWS, self-managed Azure, and GCP.
	if (opts.TokenType == CloudToken || opts.TokenType == CloudAndAPIServerToken) &&
		(cpContext.HCP.Spec.Platform.Type == hyperv1.AWSPlatform || azureutil.IsSelfManagedAzure(cpContext.HCP.Spec.Platform.Type) || cpContext.HCP.Spec.Platform.Type == hyperv1.GCPPlatform) {
		tokenVolume := opts.buildVolume(string(CloudToken))
		podSpec.Volumes = append(podSpec.Volumes, tokenVolume)

		container := opts.buildContainer(cpContext.HCP, CloudToken, image, tokenVolume)
		opts.injectContainer(cpContext.NativeSidecarContainersEnabled, podSpec, container, cloudTokenFileMountPath, tokenVolume.Name)
	}

	if opts.TokenType == KubeAPIServerToken || opts.TokenType == CloudAndAPIServerToken {
		tokenVolume := opts.buildVolume(string(KubeAPIServerToken))
		podSpec.Volumes = append(podSpec.Volumes, tokenVolume)

		container := opts.buildContainer(cpContext.HCP, KubeAPIServerToken, image, tokenVolume)
		opts.injectContainer(cpContext.NativeSidecarContainersEnabled, podSpec, container, kubeAPITokenFileMountPath, tokenVolume.Name)
	}
}

// injectContainer adds the token-minter container to the pod spec.
// - OneShot minters are injected as regular init containers, which run to completion before main containers start.
// - When native sidecar containers are supported (K8s >= 1.29), it injects as an init container with
//   RestartPolicy=Always and a StartupProbe that blocks main containers until the token file exists.
// - Otherwise, it falls back to a regular sidecar container.
func (opts TokenMinterContainerOptions) injectContainer(nativeSidecarsEnabled bool, podSpec *corev1.PodSpec, container corev1.Container, mainContainerMountPath string, volumeName string) {
	if opts.OneShot {
		// OneShot minters run once and exit. As a regular init container, K8s guarantees
		// they complete before main containers start, so no probe is needed.
		podSpec.InitContainers = append(podSpec.InitContainers, container)
	} else if nativeSidecarsEnabled {
		restartAlways := corev1.ContainerRestartPolicyAlways
		container.RestartPolicy = &restartAlways
		container.StartupProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				Exec: &corev1.ExecAction{
					// The token-minter always writes to cloudTokenFileMountPath regardless of token type.
					Command: []string{"test", "-f", path.Join(cloudTokenFileMountPath, "token")},
				},
			},
			PeriodSeconds:    1,
			FailureThreshold: 30,
		}
		podSpec.InitContainers = append(podSpec.InitContainers, container)
	} else {
		podSpec.Containers = append(podSpec.Containers, container)
	}

	if len(podSpec.Containers) == 0 {
		panic("injectContainer: podSpec.Containers must have at least one container")
	}
	podSpec.Containers[0].VolumeMounts = append(podSpec.Containers[0].VolumeMounts, corev1.VolumeMount{
		Name:      volumeName,
		MountPath: mainContainerMountPath,
	})
}

func (opts TokenMinterContainerOptions) buildContainer(hcp *hyperv1.HostedControlPlane, tokenType TokenType, image string, tokenVolume corev1.Volume) corev1.Container {
	tokenFileMountPath := "/var/run/secrets/openshift/serviceaccount"

	var audience string
	switch tokenType {
	case CloudToken:
		audience = "openshift"
	case KubeAPIServerToken:
		audience = hcp.Spec.IssuerURL
	}
	args := []string{
		fmt.Sprintf("--token-audience=%s", audience),
		fmt.Sprintf("--service-account-namespace=%s", opts.ServiceAccountNameSpace),
		fmt.Sprintf("--service-account-name=%s", opts.ServiceAccountName),
		fmt.Sprintf("--token-file=%s", path.Join(tokenFileMountPath, "token")),
	}
	if opts.OneShot {
		args = append(args, "--oneshot")
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
				corev1.ResourceMemory: resource.MustParse("30Mi"),
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      tokenVolume.Name,
				MountPath: tokenFileMountPath,
			},
		},
	}

	if opts.KubeconfigSecretName != "" {
		container.Args = append(container.Args, fmt.Sprintf("--kubeconfig-secret-namespace=%s", hcp.Namespace))
		container.Args = append(container.Args, fmt.Sprintf("--kubeconfig-secret-name=%s", opts.KubeconfigSecretName))
	} else {
		kubeconfigMountPath := "/etc/kubernetes"
		kubeconfingVolumeName := opts.KubeconfingVolumeName
		if kubeconfingVolumeName == "" {
			kubeconfingVolumeName = "kubeconfig"
		}

		container.Args = append(container.Args, fmt.Sprintf("--kubeconfig=%s", path.Join(kubeconfigMountPath, util.KubeconfigKey)))
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
			Name:      kubeconfingVolumeName,
			MountPath: kubeconfigMountPath,
		})
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
