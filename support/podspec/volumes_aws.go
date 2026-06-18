package podspec

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"
)

// Shared constants for the combined AWS CA bundle setup. These are used by
// DeploymentAddAWSCABundleVolume, ContainerAddAWSCABundle, and
// applyAWSCABundleToKMSContainers in kas/deployment.go.
const (
	AWSCABundleVolumeName = "aws-ca-bundle"
	AWSCABundleMountPath  = "/etc/pki/ca-trust/extracted/hypershift"
	AWSCABundleFileName   = "combined-ca-bundle.pem"
)

const (
	userCAVolumeName   = "user-ca-bundle"
	userCAMountPath    = "/user-ca"
	userCAFileName     = "user-ca-bundle.pem"
	systemCABundlePath = "/etc/pki/tls/certs/ca-bundle.crt"
	initContainerName  = "setup-aws-ca-bundle"
)

// DeploymentAddAWSCABundleSetup adds the volumes and init container needed to produce a combined
// CA bundle (system CAs + user-provided additionalTrustBundle CAs). It does NOT wire
// the bundle into any application container — call ContainerAddAWSCABundle on each
// container that needs the AWS_CA_BUNDLE env var.
//
// The initContainerImage should be a RHEL-based image that has /bin/sh and cat available
// (e.g. the control-plane-operator image).
func DeploymentAddAWSCABundleSetup(trustBundleConfigMap *corev1.LocalObjectReference, deployment *appsv1.Deployment, initContainerImage string) {
	// Volume for user CAs from additionalTrustBundle ConfigMap.
	deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes, corev1.Volume{
		Name: userCAVolumeName,
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: *trustBundleConfigMap,
				Items:                []corev1.KeyToPath{{Key: "ca-bundle.crt", Path: userCAFileName}},
			},
		},
	})

	// EmptyDir volume for the combined (system + user) CA bundle.
	deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes, corev1.Volume{
		Name: AWSCABundleVolumeName,
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{},
		},
	})

	// Init container concatenates system CAs with user CAs into the combined bundle.
	deployment.Spec.Template.Spec.InitContainers = append(deployment.Spec.Template.Spec.InitContainers, corev1.Container{
		Name:  initContainerName,
		Image: initContainerImage,
		Command: []string{"/bin/sh", "-c",
			"cat " + systemCABundlePath + " " + userCAMountPath + "/" + userCAFileName +
				" > " + AWSCABundleMountPath + "/" + AWSCABundleFileName +
				" 2>/dev/null || cp " + userCAMountPath + "/" + userCAFileName +
				" " + AWSCABundleMountPath + "/" + AWSCABundleFileName},
		VolumeMounts: []corev1.VolumeMount{
			{Name: userCAVolumeName, MountPath: userCAMountPath, ReadOnly: true},
			{Name: AWSCABundleVolumeName, MountPath: AWSCABundleMountPath},
		},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10m"),
				corev1.ResourceMemory: resource.MustParse("10Mi"),
			},
		},
		SecurityContext: &corev1.SecurityContext{
			AllowPrivilegeEscalation: ptr.To(false),
			Capabilities: &corev1.Capabilities{
				Drop: []corev1.Capability{"ALL"},
			},
		},
	})
}

// ContainerAddAWSCABundle adds the combined CA bundle volume mount and AWS_CA_BUNDLE
// environment variable to a container. The deployment must already have the volumes
// and init container set up via DeploymentAddAWSCABundleSetup.
func ContainerAddAWSCABundle(c *corev1.Container) {
	c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{
		Name:      AWSCABundleVolumeName,
		MountPath: AWSCABundleMountPath,
		ReadOnly:  true,
	})
	c.Env = append(c.Env, corev1.EnvVar{
		Name:  "AWS_CA_BUNDLE",
		Value: AWSCABundleMountPath + "/" + AWSCABundleFileName,
	})
}

// DeploymentAddAWSCABundleVolume creates a combined CA bundle containing both the system CAs from
// the container image and the user-provided additionalTrustBundle CAs, then wires it into
// Containers[0]. An init container concatenates /etc/pki/tls/certs/ca-bundle.crt (system CAs)
// with the user CAs into a single file. This is necessary because the AWS SDK replaces the default
// system CA bundle when AWS_CA_BUNDLE is set, rather than appending to it.
//
// For deployments where only specific sidecar containers need AWS_CA_BUNDLE (e.g. KAS with
// KMS sidecars), use DeploymentAddAWSCABundleSetup and ContainerAddAWSCABundle separately.
//
// The initContainerImage should be a RHEL-based image that has /bin/sh and cat available
// (e.g. the control-plane-operator image).
func DeploymentAddAWSCABundleVolume(trustBundleConfigMap *corev1.LocalObjectReference, deployment *appsv1.Deployment, initContainerImage string) {
	DeploymentAddAWSCABundleSetup(trustBundleConfigMap, deployment, initContainerImage)
	ContainerAddAWSCABundle(&deployment.Spec.Template.Spec.Containers[0])
}
