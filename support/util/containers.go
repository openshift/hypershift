package util

import (
	"fmt"
	"reflect"
	"slices"

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

func RemoveContainer(name string, podSpec *corev1.PodSpec) {
	podSpec.Containers = slices.DeleteFunc(podSpec.Containers, func(c corev1.Container) bool {
		return c.Name == name
	})
}

func RemoveInitContainer(name string, podSpec *corev1.PodSpec) {
	podSpec.InitContainers = slices.DeleteFunc(podSpec.InitContainers, func(c corev1.Container) bool {
		return c.Name == name
	})
}

func RemoveContainerVolumeMount(name string, container *corev1.Container) {
	container.VolumeMounts = slices.DeleteFunc(container.VolumeMounts, func(v corev1.VolumeMount) bool {
		return v.Name == name
	})
}

func UpsertEnvVar(c *corev1.Container, envVar corev1.EnvVar) {
	for idx, env := range c.Env {
		if env.Name == envVar.Name {
			c.Env[idx].Value = envVar.Value
			return
		}
	}
	c.Env = append(c.Env, envVar)
}

func UpsertEnvVars(c *corev1.Container, envVars []corev1.EnvVar) {
	for _, v := range envVars {
		UpsertEnvVar(c, v)
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
		availabilityProberContainer.Command = append(availabilityProberContainer.Command, "--wait-for-infrastructure-resource")
	}
	if opts.WaitForLabeledPodsGone != "" {
		availabilityProberContainer.Command = append(availabilityProberContainer.Command, fmt.Sprintf("--wait-for-labeled-pods-gone=%s", opts.WaitForLabeledPodsGone))
	}
	if opts.WaitForClusterRolebinding != "" {
		availabilityProberContainer.Command = append(availabilityProberContainer.Command, fmt.Sprintf("--wait-for-cluster-rolebinding=%s", opts.WaitForClusterRolebinding))
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
	WaitForClusterRolebinding     string
}

type AvailabilityProberOpt func(*AvailabilityProberOpts)

func WithOptions(opts *AvailabilityProberOpts) AvailabilityProberOpt {
	return func(o *AvailabilityProberOpts) {
		o.KubeconfigVolumeName = opts.KubeconfigVolumeName
		o.RequiredAPIs = opts.RequiredAPIs
		o.WaitForInfrastructureResource = opts.WaitForInfrastructureResource
		o.WaitForLabeledPodsGone = opts.WaitForLabeledPodsGone
		o.WaitForClusterRolebinding = opts.WaitForClusterRolebinding
	}
}
