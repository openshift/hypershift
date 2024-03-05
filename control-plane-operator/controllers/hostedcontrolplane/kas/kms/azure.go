package kms

import (
	"fmt"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/azure"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	v1 "k8s.io/apiserver/pkg/apis/config/v1"
	"k8s.io/utils/ptr"
)

const (
	azureKMSUnixSocketFileName    = "azurekms.socket"
	azureKMSCredsFileKey          = "azure.json"
	azureProviderConfigNamePrefix = "azure"

	azureKMSHealthPort = 8787

	// https://github.com/Azure/kubernetes-kms
	// TODO: get image from payload
	azureKMSProviderImage = "mcr.microsoft.com/oss/azure/kms/keyvault:v0.5.0"
)

var (
	azureKMSVolumeMounts = util.PodVolumeMounts{
		KasMainContainerName: {
			kasVolumeKMSSocket().Name: "/opt",
		},
		kasContainerAzureKMS().Name: {
			kasVolumeKMSSocket().Name:           "/opt",
			kasVolumeAzureKMSCredentials().Name: "/etc/kubernetes",
		},
	}
	azureKMSUnixSocket = fmt.Sprintf("unix://%s/%s", azureKMSVolumeMounts.Path(KasMainContainerName, kasVolumeKMSSocket().Name), azureKMSUnixSocketFileName)
)

var _ IKMSProvider = &azureKMSProvider{}

type azureKMSProvider struct {
	kmsSpec  *hyperv1.AzureKMSSpec
	kmsImage string
}

func NewAzureKMSProvider(kmsSpec *hyperv1.AzureKMSSpec) (*azureKMSProvider, error) {
	if kmsSpec == nil {
		return nil, fmt.Errorf("azure kms metadata not specified")
	}
	return &azureKMSProvider{
		kmsSpec: kmsSpec,
		// TODO: get image from payload
		kmsImage: azureKMSProviderImage,
	}, nil
}

func (p *azureKMSProvider) GenerateKMSEncryptionConfig() (*v1.EncryptionConfiguration, error) {
	var providerConfiguration []v1.ProviderConfiguration

	providerConfiguration = append(providerConfiguration, v1.ProviderConfiguration{
		KMS: &v1.KMSConfiguration{
			Name:      fmt.Sprintf("%s-%s", azureProviderConfigNamePrefix, p.kmsSpec.KeyVaultName),
			Endpoint:  azureKMSUnixSocket,
			CacheSize: ptr.To[int32](100),
			Timeout:   &metav1.Duration{Duration: 35 * time.Second},
		},
	})

	providerConfiguration = append(providerConfiguration, v1.ProviderConfiguration{
		Identity: &v1.IdentityConfiguration{},
	})
	encryptionConfig := &v1.EncryptionConfiguration{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1.SchemeGroupVersion.String(),
			Kind:       encryptionConfigurationKind,
		},
		Resources: []v1.ResourceConfiguration{
			{
				Resources: config.KMSEncryptedObjects(),
				Providers: providerConfiguration,
			},
		},
	}
	return encryptionConfig, nil
}

func (p *azureKMSProvider) ApplyKMSConfig(podSpec *corev1.PodSpec, deploymentConfig config.DeploymentConfig) error {
	podSpec.Volumes = append(podSpec.Volumes,
		util.BuildVolume(kasVolumeAzureKMSCredentials(), buildVolumeAzureKMSCredentials),
		util.BuildVolume(kasVolumeKMSSocket(), buildVolumeKMSSocket),
	)

	podSpec.Containers = append(podSpec.Containers, util.BuildContainer(kasContainerAzureKMS(), p.buildKASContainerAzureKMS))
	deploymentConfig.LivenessProbes[kasContainerAzureKMS().Name] = corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			HTTPGet: &corev1.HTTPGetAction{
				Scheme: corev1.URISchemeHTTP,
				Port:   intstr.FromInt(azureKMSHealthPort),
				Path:   "/healthz",
			},
		},
	}

	var container *corev1.Container
	for i, c := range podSpec.Containers {
		if c.Name == KasMainContainerName {
			container = &podSpec.Containers[i]
			break
		}
	}
	if container == nil {
		panic("main kube apiserver container not found in spec")
	}
	container.VolumeMounts = append(container.VolumeMounts,
		azureKMSVolumeMounts.ContainerMounts(KasMainContainerName)...)

	return nil
}

func (p *azureKMSProvider) buildKASContainerAzureKMS(c *corev1.Container) {
	c.Image = p.kmsImage
	c.ImagePullPolicy = corev1.PullIfNotPresent
	c.Ports = []corev1.ContainerPort{
		{
			Name:          "http",
			ContainerPort: int32(azureKMSHealthPort),
			Protocol:      corev1.ProtocolTCP,
		},
	}

	c.Args = []string{
		fmt.Sprintf("--keyvault-name=%s", p.kmsSpec.KeyVaultName),
		fmt.Sprintf("--key-name=%s", p.kmsSpec.KeyName),
		fmt.Sprintf("--key-version=%s", p.kmsSpec.KeyVersion),
		fmt.Sprintf("--listen-addr=%s", azureKMSUnixSocket),
		fmt.Sprintf("--healthz-port=%d", azureKMSHealthPort),
		"--healthz-path=/healthz",
		fmt.Sprintf("--config-file-path=%s/%s", azureKMSVolumeMounts.Path(c.Name, kasVolumeAzureKMSCredentials().Name), azureKMSCredsFileKey),
		"-v=1",
	}
	c.VolumeMounts = azureKMSVolumeMounts.ContainerMounts(c.Name)
	c.Resources = corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceMemory: resource.MustParse("10Mi"),
			corev1.ResourceCPU:    resource.MustParse("10m"),
		},
	}
}

func kasContainerAzureKMS() *corev1.Container {
	return &corev1.Container{
		Name: "azure-kms-provider",
	}
}

func kasVolumeAzureKMSCredentials() *corev1.Volume {
	return &corev1.Volume{
		Name: "azure-kms-credentials",
	}
}

func buildVolumeAzureKMSCredentials(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: manifests.AzureProviderConfigWithCredentials("").Name,
		Items: []corev1.KeyToPath{
			{
				Key:  azure.CloudConfigKey,
				Path: azureKMSCredsFileKey,
			},
		},
	}
}
