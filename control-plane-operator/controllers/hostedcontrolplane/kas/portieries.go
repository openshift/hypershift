package kas

import (
	"github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
)

const portierisPort = 8000

var (
	portieriesVolumeMounts = util.PodVolumeMounts{
		kasContainerPortieries().Name: {
			kasVolumeLocalhostKubeconfig().Name: "/etc/openshift/kubeconfig",
			kasVolumePortierisCerts().Name:      "/etc/certs",
		},
	}
)

func applyPortieriesConfig(podSpec *corev1.PodSpec, portieriesImage string) {
	podSpec.Containers = append(podSpec.Containers, util.BuildContainer(kasContainerPortieries(), buildKASContainerPortieries(portieriesImage)))
	podSpec.Volumes = append(podSpec.Volumes, util.BuildVolume(kasVolumePortierisCerts(), buildKASVolumePortierisCerts))
}

func kasContainerPortieries() *corev1.Container {
	return &corev1.Container{
		Name: "portieris",
	}
}

func buildKASContainerPortieries(image string) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = image
		c.ImagePullPolicy = corev1.PullIfNotPresent
		c.TerminationMessagePolicy = corev1.TerminationMessageFallbackToLogsOnError
		c.Command = []string{
			"/portieris",
		}
		c.Args = []string{
			"--kubeconfig=/etc/openshift/kubeconfig/kubeconfig",
			"--alsologtostderr",
			"-v=4",
		}
		c.Ports = []corev1.ContainerPort{
			{
				Name:          "http",
				ContainerPort: portierisPort,
				Protocol:      corev1.ProtocolTCP,
			},
		}
		c.VolumeMounts = portieriesVolumeMounts.ContainerMounts(c.Name)
	}
}

func kasVolumePortierisCerts() *corev1.Volume {
	return &corev1.Volume{
		Name: "portieris-certs",
	}
}

func buildKASVolumePortierisCerts(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName:  v.Name,
		DefaultMode: ptr.To[int32](0640),
	}
}
