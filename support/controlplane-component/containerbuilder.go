package controlplanecomponent

import (
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

type containerBuilder struct {
	name    string
	image   string
	command []string
	args    []string

	memoryResourcesRequest resource.Quantity
	cpuResourcesRequest    resource.Quantity

	env   []corev1.EnvVar
	ports []corev1.ContainerPort

	livnessProbe   *corev1.Probe
	readinessProbe *corev1.Probe
}

func NewContainer(name string) *containerBuilder {
	return &containerBuilder{
		name: name,
	}
}

func (b *containerBuilder) Image(image string) *containerBuilder {
	b.image = image
	return b
}

func (b *containerBuilder) Command(cmd ...string) *containerBuilder {
	b.command = cmd
	return b
}

func (b *containerBuilder) WithArgs(args ...string) *containerBuilder {
	b.args = append(b.args, args...)
	return b
}

func (b *containerBuilder) WithStringEnv(name, value string) *containerBuilder {
	b.env = append(b.env, corev1.EnvVar{
		Name:  name,
		Value: value,
	})
	return b
}

func (b *containerBuilder) WithEnv(name string, source *corev1.EnvVarSource) *containerBuilder {
	b.env = append(b.env, corev1.EnvVar{
		Name:      name,
		ValueFrom: source,
	})
	return b
}

func (b *containerBuilder) WithPort(port corev1.ContainerPort) *containerBuilder {
	b.ports = append(b.ports, port)
	return b
}

func (b *containerBuilder) WithMemoryResourcesRequest(quantity resource.Quantity) *containerBuilder {
	b.memoryResourcesRequest = quantity
	return b
}

func (b *containerBuilder) WithCPUResourcesRequest(quantity resource.Quantity) *containerBuilder {
	b.cpuResourcesRequest = quantity
	return b
}

func (b *containerBuilder) WithHTTPLivnessProbe(action *corev1.HTTPGetAction) *containerBuilder {
	b.livnessProbe = &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			HTTPGet: action,
		},
		// unified values for all containers
		InitialDelaySeconds: 30,
		PeriodSeconds:       60,
		SuccessThreshold:    1,
		FailureThreshold:    5,
		TimeoutSeconds:      5,
	}
	return b
}

func (b *containerBuilder) WithHTTPReadinessProbe(action *corev1.HTTPGetAction) *containerBuilder {
	b.readinessProbe = &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			HTTPGet: action,
		},
		// unified values for all containers
		PeriodSeconds:    10,
		SuccessThreshold: 1,
		FailureThreshold: 3,
		TimeoutSeconds:   5,
	}
	return b
}

func (b *containerBuilder) Build() corev1.Container {
	b.validate()
	b.setDefaults()

	return corev1.Container{
		Name:            b.name,
		Image:           b.image,
		Command:         b.command,
		Args:            b.args,
		ImagePullPolicy: corev1.PullIfNotPresent,
		Env:             b.env,
		Ports:           b.ports,
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    b.cpuResourcesRequest,
				corev1.ResourceMemory: b.memoryResourcesRequest,
			},
		},
		LivenessProbe:  b.livnessProbe,
		ReadinessProbe: b.readinessProbe,
	}
}

func (b *containerBuilder) setDefaults() {
	if b.cpuResourcesRequest.IsZero() {
		b.cpuResourcesRequest = resource.MustParse("100m")
	}

	if b.memoryResourcesRequest.IsZero() {
		b.memoryResourcesRequest = resource.MustParse("100Mi")
	}
}

func (b *containerBuilder) validate() {
	if b.name == "" {
		panic("name is required")
	}
	if b.image == "" {
		panic("image is required")
	}
	if len(b.command) == 0 {
		panic("command is required")
	}
}
