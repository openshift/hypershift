package util

import (
	"fmt"
	"path"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	awsCloudProviderCredsKey = "credentials"
	KubeconfigKey            = "kubeconfig"
	AWSCloudProviderName     = "aws"
)

func cloudProviderCredsVolumeMount(containerName string) PodVolumeMounts {
	return PodVolumeMounts{
		containerName: {
			cloudProviderCredsVolume().Name: "/etc/kubernetes/secrets/cloud-provider",
		},
	}
}
func cloudProviderTokenVolumeMount(containerName string) PodVolumeMounts {
	return PodVolumeMounts{
		containerName: {
			cloudProviderTokenVolume().Name: "/var/run/secrets/openshift/serviceaccount",
		},
		cloudProviderInitContainerTokenMinter().Name: {
			cloudProviderTokenVolume().Name:           "/var/run/secrets/openshift/serviceaccount",
			cloudProviderTokenKubeconfigVolume().Name: "/etc/kubernetes",
		},
	}
}

func ApplyCloudProviderCreds(
	podSpec *corev1.PodSpec,
	cloudProvider string,
	cloudProviderCreds *corev1.LocalObjectReference,
	tokenMinterImage string,
	containerName string,
) {
	if cloudProviderCreds == nil || cloudProviderCreds.Name == "" {
		return
	}
	podSpec.Volumes = append(podSpec.Volumes, BuildVolume(cloudProviderCredsVolume(), buildCloudProviderCreds(cloudProviderCreds.Name)))
	container := mustContainer(podSpec, containerName)
	container.VolumeMounts = append(container.VolumeMounts, cloudProviderCredsVolumeMount(containerName).ContainerMounts(containerName)...)

	switch cloudProvider {
	case AWSCloudProviderName:
		credsPath := path.Join(cloudProviderCredsVolumeMount(containerName).Path(containerName, cloudProviderCredsVolume().Name), awsCloudProviderCredsKey)
		container.Env = append(container.Env,
			corev1.EnvVar{
				Name:  "AWS_SHARED_CREDENTIALS_FILE",
				Value: credsPath,
			},
			corev1.EnvVar{
				Name:  "AWS_SDK_LOAD_CONFIG",
				Value: "true",
			},
			corev1.EnvVar{
				Name:  "AWS_EC2_METADATA_DISABLED",
				Value: "false",
			})
		podSpec.Volumes = append(podSpec.Volumes, BuildVolume(cloudProviderTokenVolume(), buildCloudProviderTokenVolume()))
		container.VolumeMounts = append(container.VolumeMounts,
			cloudProviderTokenVolumeMount(containerName).ContainerMounts(containerName)...)
		tokenMinterContainer := BuildContainer(cloudProviderInitContainerTokenMinter(), buildCloudProviderTokenMinterContainer(tokenMinterImage, tokenMinterArgs()))
		podSpec.Containers = append(podSpec.Containers, tokenMinterContainer)
	}
}

func cloudProviderCredsVolume() *corev1.Volume {
	return &corev1.Volume{
		Name: "cloud-creds",
	}
}

func buildCloudProviderCreds(cloudProviderCredsName string) func(v *corev1.Volume) {
	return func(v *corev1.Volume) {
		v.Secret = &corev1.SecretVolumeSource{}
		v.Secret.SecretName = cloudProviderCredsName
	}
}

func cloudProviderTokenVolume() *corev1.Volume {
	return &corev1.Volume{
		Name: "cloud-token",
	}
}

func buildCloudProviderTokenVolume() func(v *corev1.Volume) {
	return func(v *corev1.Volume) {
		v.EmptyDir = &corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumMemory}
	}
}

func cloudProviderInitContainerTokenMinter() *corev1.Container {
	return &corev1.Container{
		Name: "token-minter",
	}
}

func cloudProviderTokenKubeconfigVolume() *corev1.Volume {
	return &corev1.Volume{
		Name: "kubeconfig",
	}
}

func buildCloudProviderTokenMinterContainer(image string, args []string) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = image
		c.ImagePullPolicy = corev1.PullIfNotPresent
		c.Command = []string{"/usr/bin/control-plane-operator", "token-minter"}
		c.Args = args
		c.Resources.Requests = corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("10m"),
			corev1.ResourceMemory: resource.MustParse("10Mi"),
		}
		c.VolumeMounts = cloudProviderTokenVolumeMount(c.Name).ContainerMounts(c.Name)
	}
}

func tokenMinterArgs() []string {
	cpath := func(vol, file string) string {
		return path.Join(cloudProviderTokenVolumeMount("").Path(cloudProviderInitContainerTokenMinter().Name, vol), file)
	}
	return []string{
		"--service-account-namespace=kube-system",
		"--service-account-name=kube-controller-manager",
		"--token-audience=openshift",
		fmt.Sprintf("--token-file=%s", cpath(cloudProviderTokenVolume().Name, "token")),
		fmt.Sprintf("--kubeconfig=%s", cpath(cloudProviderTokenKubeconfigVolume().Name, KubeconfigKey)),
	}
}

func mustContainer(podSpec *corev1.PodSpec, name string) *corev1.Container {
	var container *corev1.Container
	for i, c := range podSpec.Containers {
		if c.Name == name {
			container = &podSpec.Containers[i]
			break
		}
	}
	if container == nil {
		panic(fmt.Sprintf("expected container %s not found pod spec", name))
	}
	return container
}
