package kas

import (
	"github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

const (
	portierisPort          = 8000
	portierisContainerName = "portieris"

	localhostKubeconfigVolumeName = "localhost-kubeconfig"
	portierisCertsVolumeName      = "portieris-certs"
)

var (
	portieriesVolumeMounts = util.PodVolumeMounts{
		portierisContainerName: {
			localhostKubeconfigVolumeName: "/etc/openshift/kubeconfig",
			portierisCertsVolumeName:      "/etc/certs",
		},
	}
)

func applyPortieriesConfig(podSpec *corev1.PodSpec, portieriesImage string) {
	podSpec.Containers = append(podSpec.Containers, buildKASPortieriesContainer(portieriesImage))
	podSpec.Volumes = append(podSpec.Volumes, buildKASPortierisCertsVolume())
}

func buildKASPortieriesContainer(image string) corev1.Container {
	return corev1.Container{
		Name:            portierisContainerName,
		Image:           image,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Command: []string{
			"/portieris",
		},
		Args: []string{
			"--kubeconfig=/etc/openshift/kubeconfig/kubeconfig",
			"--alsologtostderr",
			"-v=4",
		},
		Ports: []corev1.ContainerPort{
			{
				Name:          "http",
				ContainerPort: portierisPort,
				Protocol:      corev1.ProtocolTCP,
			},
		},
		VolumeMounts: portieriesVolumeMounts.ContainerMounts(portierisContainerName),
		LivenessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Scheme: corev1.URISchemeHTTPS,
					Port:   intstr.FromInt(portierisPort),
					Path:   "/health/liveness",
				},
			},
			InitialDelaySeconds: 120,
			PeriodSeconds:       300,
			TimeoutSeconds:      160,
			FailureThreshold:    3,
			SuccessThreshold:    1,
		},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("20Mi"),
				corev1.ResourceCPU:    resource.MustParse("5m"),
			},
		},
	}
}

func buildKASPortierisCertsVolume() corev1.Volume {
	v := corev1.Volume{
		Name: portierisCertsVolumeName,
	}
	v.Secret = &corev1.SecretVolumeSource{
		SecretName:  v.Name,
		DefaultMode: ptr.To[int32](0640),
	}
	return v
}
