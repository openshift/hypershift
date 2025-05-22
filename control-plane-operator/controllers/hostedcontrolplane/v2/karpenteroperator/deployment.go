package karpenteroperator

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/support/util"

	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/kas"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	component "github.com/openshift/hypershift/support/controlplane-component"

	appsv1 "k8s.io/api/apps/v1"
)

func (karp *KarpenterOperatorOptions) adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	hcp := cpContext.HCP

	util.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		c.Image = karp.HyperShiftOperatorImage
	})

	switch hcp.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes,
			corev1.Volume{
				Name: "serviceaccount-token",
				VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{
						Medium: corev1.StorageMediumMemory,
					},
				},
			},
			corev1.Volume{
				Name: "provider-creds",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: "karpenter-credentials",
					},
				},
			},
		)
		util.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
			c.Env = append(c.Env,
				corev1.EnvVar{
					Name:  "AWS_SHARED_CREDENTIALS_FILE",
					Value: "/etc/provider/credentials",
				},
				corev1.EnvVar{
					Name:  "AWS_REGION",
					Value: hcp.Spec.Platform.AWS.Region,
				},
				corev1.EnvVar{
					Name:  "AWS_SDK_LOAD_CONFIG",
					Value: "true",
				},
			)
			c.VolumeMounts = append(c.VolumeMounts,
				corev1.VolumeMount{
					Name:      "serviceaccount-token",
					MountPath: "/var/run/secrets/openshift/serviceaccount",
				},
				corev1.VolumeMount{
					Name:      "provider-creds",
					MountPath: "/etc/provider",
				},
			)
			c.Args = append(c.Args,
				"--control-plane-operator-image="+karp.ControlPlaneOperatorImage,
				"--karpenter-provider-aws-image="+karp.KarpenterProviderAWSImage,
			)
		})
	}

	deployment.Spec.Template.Spec.Containers = append(deployment.Spec.Template.Spec.Containers,
		corev1.Container{
			Name:            "token-minter",
			Image:           karp.ControlPlaneOperatorImage,
			ImagePullPolicy: corev1.PullIfNotPresent,
			Command:         []string{"/usr/bin/control-plane-operator", "token-minter"},
			Args: []string{
				"--service-account-namespace=kube-system",
				"--service-account-name=karpenter",
				"--token-file=/var/run/secrets/openshift/serviceaccount/token",
				fmt.Sprintf("--kubeconfig-secret-namespace=%s", deployment.Namespace),
				"--kubeconfig-secret-name=service-network-admin-kubeconfig",
			},
			Resources: corev1.ResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceCPU:    resource.MustParse("10m"),
					corev1.ResourceMemory: resource.MustParse("30Mi"),
				},
			},
			VolumeMounts: []corev1.VolumeMount{
				{
					Name:      "serviceaccount-token",
					MountPath: "/var/run/secrets/openshift/serviceaccount",
				},
			},
		},
	)

	util.AvailabilityProber(kas.InClusterKASReadyURL(hcp.Spec.Platform.Type), karp.ControlPlaneOperatorImage, &deployment.Spec.Template.Spec)

	return nil
}
