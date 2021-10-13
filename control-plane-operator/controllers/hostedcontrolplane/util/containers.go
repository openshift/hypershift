package util

import (
	"reflect"

	corev1 "k8s.io/api/core/v1"
)

func BuildContainer(container *corev1.Container, buildFn func(*corev1.Container)) corev1.Container {
	buildFn(container)
	return *container
}

// ApplyContainer will add or update container within containers and return an
// array of containers with the mutated container.
func ApplyContainer(containers []corev1.Container, container *corev1.Container, buildFn func(*corev1.Container)) []corev1.Container {
	for _, existing := range containers {
		if existing.Name == container.Name {
			buildFn(&existing)
			return containers
		}
	}
	buildFn(container)
	return append(containers, *container)
}

// AvailabilityProberImageName is the name under which components can find the availability prober
// image in the release image.
const AvailabilityProberImageName = "availability-prober"

func AvailabilityProber(target string, image string, spec *corev1.PodSpec) {
	availabilityProberContainer := corev1.Container{
		Name:                     "availability-prober",
		Image:                    image,
		ImagePullPolicy:          corev1.PullAlways,
		TerminationMessagePath:   corev1.TerminationMessagePathDefault,
		TerminationMessagePolicy: corev1.TerminationMessageReadFile,
		Command: []string{
			"/usr/bin/availability-prober",
			"--target",
			target,
		},
	}
	if len(spec.InitContainers) == 0 || spec.InitContainers[0].Name != "availability-prober" {
		spec.InitContainers = append([]corev1.Container{{}}, spec.InitContainers...)
	}
	if !reflect.DeepEqual(spec.InitContainers[0], availabilityProberContainer) {
		spec.InitContainers[0] = availabilityProberContainer
	}
}
