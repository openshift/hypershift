package util

import (
	"fmt"
	"reflect"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func BuildContainer(container *corev1.Container, buildFn func(*corev1.Container)) corev1.Container {
	buildFn(container)
	return *container
}

func FindContainer(name string, containers []corev1.Container) *corev1.Container {
	for i, c := range containers {
		if c.Name == name {
			return &containers[i]
		}
	}
	return nil
}

func UpdateContainer(name string, containers []corev1.Container, update func(c *corev1.Container)) {
	for i, c := range containers {
		if c.Name == name {
			update(&containers[i])
		}
	}
}

const (

	// CPOImageName is the name under which components can find the CPO image in the release image..
	CPOImageName = "controlplane-operator"

	// CPPKIOImageName is the name under which components can find the CP PKI Operator image in the release image..
	CPPKIOImageName = "controlplane-pki-operator"

	// AvailabilityProberImageName is the name under which components can find the availability prober
	// image in the release image.
	AvailabilityProberImageName = "availability-prober"
)

func AvailabilityProber(target string, image string, spec *corev1.PodSpec, o ...AvailabilityProberOpt) {
	opts := AvailabilityProberOpts{}
	for _, opt := range o {
		opt(&opts)
	}
	availabilityProberContainer := corev1.Container{
		Name:            "availability-prober",
		Image:           image,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Command: []string{
			"/usr/bin/control-plane-operator",
			"availability-prober",
			"--target",
			target,
		},
	}
	if opts.KubeconfigVolumeName != "" {
		availabilityProberContainer.VolumeMounts = append(availabilityProberContainer.VolumeMounts, corev1.VolumeMount{
			Name:      opts.KubeconfigVolumeName,
			MountPath: "/var/kubeconfig",
		})
		availabilityProberContainer.Command = append(availabilityProberContainer.Command, "--kubeconfig=/var/kubeconfig/kubeconfig")
		for _, api := range opts.RequiredAPIs {
			availabilityProberContainer.Command = append(availabilityProberContainer.Command, fmt.Sprintf("--required-api=%s,%s,%s", api.Group, api.Version, api.Kind))
		}
	}
	if opts.WaitForInfrastructureResource {
		availabilityProberContainer.Command = append(availabilityProberContainer.Command, fmt.Sprintf("--wait-for-infrastructure-resource"))
	}
	if opts.WaitForLabeledPodsGone != "" {
		availabilityProberContainer.Command = append(availabilityProberContainer.Command, fmt.Sprintf("--wait-for-labeled-pods-gone=%s", opts.WaitForLabeledPodsGone))
	}
	if len(spec.InitContainers) == 0 || spec.InitContainers[0].Name != "availability-prober" {
		spec.InitContainers = append([]corev1.Container{{}}, spec.InitContainers...)
	}
	if !reflect.DeepEqual(spec.InitContainers[0], availabilityProberContainer) {
		spec.InitContainers[0] = availabilityProberContainer
	}
}

type AvailabilityProberOpts struct {
	KubeconfigVolumeName          string
	RequiredAPIs                  []schema.GroupVersionKind
	WaitForInfrastructureResource bool
	WaitForLabeledPodsGone        string
}

type AvailabilityProberOpt func(*AvailabilityProberOpts)

func WithOptions(opts *AvailabilityProberOpts) AvailabilityProberOpt {
	return func(o *AvailabilityProberOpts) {
		o.KubeconfigVolumeName = opts.KubeconfigVolumeName
		o.RequiredAPIs = opts.RequiredAPIs
		o.WaitForInfrastructureResource = opts.WaitForInfrastructureResource
		o.WaitForLabeledPodsGone = opts.WaitForLabeledPodsGone
	}
}
