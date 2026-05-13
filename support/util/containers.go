package util

import (
	"fmt"
	"reflect"
	"slices"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/utils/ptr"
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

	// PodTmpDirMountName is a name for a volume created in each pod by the CPO that gives the pods containers a place to mount and write temporary files to.
	PodTmpDirMountName = "tmp-dir"
	// PodTmpDirMountPath is the path that each container created by the CPO will mount the volume PodTmpDirMountName at.
	PodTmpDirMountPath = "/tmp"
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

// KASReadinessCheckContainer returns a sidecar container that probes the KAS /livez endpoint.
// When KAS is unreachable, the readiness probe fails and the pod goes unready,
// which prevents PDB from treating it as healthy during eviction decisions.
// This uses /livez (not /readyz) to avoid a circular dependency: KAS /readyz checks
// aggregated API servers, which include OAS and OAuth API Server.
func KASReadinessCheckContainer(kasLivezURL string) corev1.Container {
	return corev1.Container{
		Name:    "kas-readiness-check",
		Image:   "cli",
		Command: []string{"/bin/bash", "-c", "sleep infinity"},
		ReadinessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				Exec: &corev1.ExecAction{
					Command: []string{"/bin/bash", "-c", fmt.Sprintf("curl -kfs %s > /dev/null", kasLivezURL)},
				},
			},
			FailureThreshold: 3,
			PeriodSeconds:    10,
			TimeoutSeconds:   5,
		},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10m"),
				corev1.ResourceMemory: resource.MustParse("10Mi"),
			},
		},
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

// enforceRestrictedSecurityContext enforces restricted security context settings on a single container.
// Per Kubernetes restricted pod security standards, only NET_BIND_SERVICE is allowed.
// See: https://kubernetes.io/docs/concepts/security/pod-security-standards/#restricted
func enforceRestrictedSecurityContext(container *corev1.Container) error {
	if container.SecurityContext == nil {
		container.SecurityContext = &corev1.SecurityContext{}
	}
	container.SecurityContext.AllowPrivilegeEscalation = ptr.To(false)
	container.SecurityContext.RunAsNonRoot = ptr.To(true)

	var existingAdd []corev1.Capability
	if container.SecurityContext.Capabilities != nil {
		existingAdd = container.SecurityContext.Capabilities.Add
		// Validate capabilities against restricted pod security standards
		for _, cap := range existingAdd {
			if cap != "NET_BIND_SERVICE" {
				return fmt.Errorf("container %q: capability %q is not allowed by restricted pod security standards (only NET_BIND_SERVICE is permitted)", container.Name, cap)
			}
		}
	}
	container.SecurityContext.Capabilities = &corev1.Capabilities{
		Drop: []corev1.Capability{"ALL"},
		Add:  existingAdd,
	}
	return nil
}

// EnforceRestrictedSecurityContextToContainers enforces restricted pod security standards
// on all containers and init containers in a PodSpec. Only NET_BIND_SERVICE capability is allowed.
func EnforceRestrictedSecurityContextToContainers(podSpec *corev1.PodSpec) error {
	// Apply to init containers
	for i := range podSpec.InitContainers {
		if err := enforceRestrictedSecurityContext(&podSpec.InitContainers[i]); err != nil {
			return err
		}
	}

	// Apply to containers
	for i := range podSpec.Containers {
		if err := enforceRestrictedSecurityContext(&podSpec.Containers[i]); err != nil {
			return err
		}
	}
	return nil
}
