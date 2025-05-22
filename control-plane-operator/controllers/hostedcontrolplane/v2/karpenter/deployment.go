package karpenter

import (
	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/util"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/utils/ptr"
)

const (
	kubeconfigVolumeName = "target-kubeconfig"
)

func adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	hcp := cpContext.HCP

	util.UpdateVolume(kubeconfigVolumeName, deployment.Spec.Template.Spec.Volumes, func(v *corev1.Volume) {
		v.Secret.SecretName = manifests.KASServiceCAPIKubeconfigSecret(hcp.Namespace, hcp.Spec.InfraID).Name
	})

	util.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		c.Env = append(c.Env,
			corev1.EnvVar{
				Name:  "AWS_REGION",
				Value: hcp.Spec.Platform.AWS.Region,
			},
			corev1.EnvVar{
				Name:  "CLUSTER_NAME",
				Value: hcp.Spec.InfraID,
			},
		)
		// Override the image if specified in the HCP annotations.
		karpenterProviderAWSOverride, exists := hcp.Annotations[hyperkarpenterv1.KarpenterProviderAWSImage]
		if exists {
			c.Image = karpenterProviderAWSOverride
		}
	})

	deployment.Spec.Template.Spec.InitContainers = append(deployment.Spec.Template.Spec.InitContainers,
		corev1.Container{
			Name:    "token-minter",
			Image:   "token-minter",
			Command: []string{"/usr/bin/control-plane-operator", "token-minter"},
			Args: []string{
				"--service-account-namespace=kube-system",
				"--service-account-name=karpenter",
				"--token-file=/var/run/secrets/openshift/serviceaccount/token",
				"--kubeconfig=/mnt/kubeconfig/target-kubeconfig",
			},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("10m"),
					corev1.ResourceMemory: resource.MustParse("30Mi"),
				},
			},
			RestartPolicy: ptr.To(corev1.ContainerRestartPolicyAlways),
			StartupProbe: &corev1.Probe{
				ProbeHandler: corev1.ProbeHandler{
					Exec: &corev1.ExecAction{
						Command: []string{"cat", "/var/run/secrets/openshift/serviceaccount/token"}, // waits until the token file is created.
					},
				},
				FailureThreshold: 10,
			},
			VolumeMounts: []corev1.VolumeMount{
				{
					Name:      "target-kubeconfig",
					MountPath: "/mnt/kubeconfig",
				},
				{
					Name:      "serviceaccount-token",
					MountPath: "/var/run/secrets/openshift/serviceaccount",
				},
			},
		},
	)

	return nil
}
