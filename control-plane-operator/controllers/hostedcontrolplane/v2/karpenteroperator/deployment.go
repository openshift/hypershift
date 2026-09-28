package karpenteroperator

import (
	"fmt"
	"os"
	"path"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	hyperkarpenterv1 "github.com/openshift/hypershift/api/karpenter/v1"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	karpenterutil "github.com/openshift/hypershift/support/karpenter"
	"github.com/openshift/hypershift/support/podspec"
	"github.com/openshift/hypershift/support/proxy"
	"github.com/openshift/hypershift/support/rhobsmonitoring"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	// ManagementClusterEnvVar signals to Karpenter Operator that it should run in management cluster mode.
	ManagementClusterEnvVar = "MANAGEMENT_CLUSTER"
	// KarpenterImageAWSEnvVar is the environment variable that sets the Karpenter image for AWS.
	KarpenterImageAWSEnvVar = "KARPENTER_IMAGE_AWS"
	// KarpenterImageAzureEnvVar is the environment variable that sets the Karpenter image for Azure.
	KarpenterImageAzureEnvVar = "KARPENTER_IMAGE_AZURE"
	// TokenMinterImageEnvVar is the environment variable that sets the Token Minter image.
	TokenMinterImageEnvVar = "TOKEN_MINTER_IMAGE"
)

func (karp *KarpenterOperatorOptions) adaptDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	if karp.StandaloneKarpenterOperatorEnabled {
		return karp.adaptStandaloneDeployment(cpContext, deployment)
	}

	hcp := cpContext.HCP

	podspec.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		c.Image = karp.HyperShiftOperatorImage
		c.Args = append(c.Args,
			"--hypershift-operator-image="+karp.HyperShiftOperatorImage,
			"--ignition-endpoint="+karp.IgnitionEndpoint,
		)
		proxy.SetEnvVars(&c.Env)
	})

	switch hcp.Spec.Platform.Type {
	case hyperv1.AWSPlatform:
		deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes,
			corev1.Volume{
				Name: "provider-creds",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: "karpenter-credentials",
					},
				},
			},
		)
		podspec.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
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
					Name:      "provider-creds",
					MountPath: "/etc/provider",
				},
			)
			c.Args = append(c.Args,
				"--control-plane-operator-image="+karp.ControlPlaneOperatorImage,
			)
			if os.Getenv(rhobsmonitoring.EnvironmentVariable) == "1" {
				c.Env = append(c.Env,
					corev1.EnvVar{
						Name:  rhobsmonitoring.EnvironmentVariable,
						Value: "1",
					},
				)
			}
		})
	}

	return nil
}

// adaptStandaloneDeployment configures the deployment for the standalone karpenter-operator binary.
func (karp *KarpenterOperatorOptions) adaptStandaloneDeployment(cpContext component.WorkloadContext, deployment *appsv1.Deployment) error {
	hcp := cpContext.HCP

	platformType := hcp.Spec.Platform.Type

	var region string
	var extraEnvVars []corev1.EnvVar
	switch platformType {
	case hyperv1.AWSPlatform:
		if hcp.Spec.Platform.AWS == nil {
			return fmt.Errorf("aws platform spec is required")
		}
		region = hcp.Spec.Platform.AWS.Region
		extraEnvVars = append(extraEnvVars,
			corev1.EnvVar{
				Name:  KarpenterImageAWSEnvVar,
				Value: cpContext.ReleaseImageProvider.GetImage(karpenterutil.KarpenterProviderAWSImageName),
			},
			corev1.EnvVar{
				Name:  "AWS_SHARED_CREDENTIALS_FILE",
				Value: "/etc/provider/credentials",
			},
			corev1.EnvVar{
				Name:  "AWS_SDK_LOAD_CONFIG",
				Value: "true",
			},
		)
	case hyperv1.AzurePlatform:
		if hcp.Spec.Platform.Azure == nil {
			return fmt.Errorf("azure platform spec is required")
		}
		clientID := string(hcp.Spec.AutoNode.Provisioner.Karpenter.Azure.ClientID)
		if clientID == "" {
			return fmt.Errorf("AutoNode Karpenter Azure clientID is required")
		}
		region = hcp.Spec.Platform.Azure.Location
		if hcp.Spec.Platform.Azure.SubnetID == "" {
			return fmt.Errorf("azure subnetID is required")
		}
		if hcp.Spec.Platform.Azure.ResourceGroupName == "" {
			return fmt.Errorf("azure resourceGroup is required")
		}
		extraEnvVars = append(extraEnvVars,
			corev1.EnvVar{
				Name:  KarpenterImageAzureEnvVar,
				Value: cpContext.ReleaseImageProvider.GetImage(karpenterutil.KarpenterProviderAzureImageName),
			},
			corev1.EnvVar{
				Name:  "AZURE_CLIENT_ID",
				Value: clientID,
			},
			corev1.EnvVar{
				Name:  "AZURE_FEDERATED_TOKEN_FILE",
				Value: path.Join(config.CloudTokenMountPath, "token"),
			},
			corev1.EnvVar{
				Name:  "AZURE_SUBSCRIPTION_ID",
				Value: hcp.Spec.Platform.Azure.SubscriptionID,
			},
			corev1.EnvVar{
				Name:  "AZURE_TENANT_ID",
				Value: hcp.Spec.Platform.Azure.TenantID,
			},
			corev1.EnvVar{
				Name:  "AZURE_NODE_RESOURCE_GROUP",
				Value: hcp.Spec.Platform.Azure.ResourceGroupName,
			},
			corev1.EnvVar{
				Name:  "VNET_SUBNET_ID",
				Value: hcp.Spec.Platform.Azure.SubnetID,
			},
		)
	}

	extraEnvVars = append(extraEnvVars, corev1.EnvVar{
		Name:  ManagementClusterEnvVar,
		Value: "true",
	}, corev1.EnvVar{
		Name:  TokenMinterImageEnvVar,
		Value: cpContext.ReleaseImageProvider.GetImage("token-minter"),
	})

	proxy.SetEnvVars(&extraEnvVars)

	if os.Getenv(rhobsmonitoring.EnvironmentVariable) == "1" {
		extraEnvVars = append(extraEnvVars,
			corev1.EnvVar{
				Name:  rhobsmonitoring.EnvironmentVariable,
				Value: "1",
			},
		)
	}

	if platformType == hyperv1.AWSPlatform {
		deployment.Spec.Template.Spec.Volumes = append(deployment.Spec.Template.Spec.Volumes,
			corev1.Volume{
				Name: "provider-creds",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: "karpenter-credentials",
					},
				},
			},
		)
	}
	podspec.UpdateContainer(ComponentName, deployment.Spec.Template.Spec.Containers, func(c *corev1.Container) {
		c.Image = cpContext.ReleaseImageProvider.GetImage(karpenterutil.KarpenterOperatorImageName)
		if override, exists := hcp.Annotations[hyperkarpenterv1.KarpenterOperatorImage]; exists && override != "" {
			c.Image = override
		}
		if platformType == hyperv1.AWSPlatform {
			c.VolumeMounts = append(c.VolumeMounts,
				corev1.VolumeMount{
					Name:      "provider-creds",
					MountPath: "/etc/provider",
				},
			)
		}
		c.Env = append(c.Env,
			corev1.EnvVar{
				Name:  "CLUSTER_NAME",
				Value: hcp.Spec.InfraID,
			},
			corev1.EnvVar{
				Name:  "CLUSTER_ENDPOINT",
				Value: fmt.Sprintf("https://%s:%d", hcp.Status.ControlPlaneEndpoint.Host, hcp.Status.ControlPlaneEndpoint.Port),
			},
			corev1.EnvVar{
				Name:  "PLATFORM",
				Value: string(platformType),
			},
			corev1.EnvVar{
				Name:  "REGION",
				Value: region,
			},
		)
		c.Env = append(c.Env,
			extraEnvVars...,
		)
	})

	return karp.addStandaloneAdapterContainer(deployment)
}

// addStandaloneAdapterContainer appends the HyperShift Karpenter adapter to the standalone Deployment.
func (karp *KarpenterOperatorOptions) addStandaloneAdapterContainer(deployment *appsv1.Deployment) error {
	adapterEnv := []corev1.EnvVar{
		{
			Name: "MY_NAMESPACE",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.namespace"},
			},
		},
		{
			Name: "MY_NAME",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"},
			},
		},
		{
			Name:  "KUBE_FEATURE_WatchListClient",
			Value: "false",
		},
	}
	proxy.SetEnvVars(&adapterEnv)

	deployment.Spec.Template.Spec.Containers = append(deployment.Spec.Template.Spec.Containers, corev1.Container{
		Name:    AdapterContainerName,
		Image:   karp.HyperShiftOperatorImage,
		Command: []string{"/usr/bin/karpenter-operator"},
		Args: []string{
			"--target-kubeconfig=/mnt/kubeconfig/target-kubeconfig",
			"--namespace=$(MY_NAMESPACE)",
			"--control-plane-operator-image=" + karp.ControlPlaneOperatorImage,
			"--hypershift-operator-image=" + karp.HyperShiftOperatorImage,
			"--ignition-endpoint=" + karp.IgnitionEndpoint,
			"--enable-standalone-karpenter-operator",
		},
		Env: adapterEnv,
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10m"),
				corev1.ResourceMemory: resource.MustParse("60Mi"),
			},
		},
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      "target-kubeconfig",
				MountPath: "/mnt/kubeconfig",
			},
		},
	})

	return nil
}
