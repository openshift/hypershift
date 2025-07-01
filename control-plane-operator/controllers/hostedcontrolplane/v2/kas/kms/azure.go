package kms

import (
	"fmt"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/azure"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/secretproviderclass"
	"github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	v1 "k8s.io/apiserver/pkg/apis/apiserver/v1"
	"k8s.io/utils/ptr"

	secretsstorev1 "sigs.k8s.io/secrets-store-csi-driver/apis/v1"
)

const (
	azureActiveKMSUnixSocketFileName = "azurekmsactive.socket"
	azureActiveKMSHealthPort         = 8787
	azureActiveKMSMetricsAddr        = "8095"

	azureBackupKMSUnixSocketFileName = "azurekmsbackup.socket"
	azureBackupKMSHealthPort         = 8788
	azureBackupKMSMetricsAddr        = "8096"

	azureKMSCredsFileKey          = "azure.json"
	azureProviderConfigNamePrefix = "azure"

	encryptedClusterSeedLocation = "/data/cluster-seed"
)

var (
	azureKMSVolumeMounts = util.PodVolumeMounts{
		KasMainContainerName: {
			kasVolumeKMSSocket().Name: "/opt",
		},
		kasContainerAzureKMSActive().Name: {
			kasVolumeKMSSocket().Name:                "/opt",
			kasVolumeAzureKMSCredentials().Name:      "/etc/kubernetes",
			kasVolumeKMSEncryptionClusterSeed().Name: "/data",
		},
		kasContainerAzureKMSBackup().Name: {
			kasVolumeKMSSocket().Name:                "/opt",
			kasVolumeAzureKMSCredentials().Name:      "/etc/kubernetes",
			kasVolumeKMSEncryptionClusterSeed().Name: "/data",
		},
	}

	azureActiveKMSUnixSocket = fmt.Sprintf("unix://%s/%s", azureKMSVolumeMounts.Path(KasMainContainerName, kasVolumeKMSSocket().Name), azureActiveKMSUnixSocketFileName)
	azureBackupKMSUnixSocket = fmt.Sprintf("unix://%s/%s", azureKMSVolumeMounts.Path(KasMainContainerName, kasVolumeKMSSocket().Name), azureBackupKMSUnixSocketFileName)
)

var _ KMSProvider = &azureKMSProvider{}

type azureKMSProvider struct {
	kmsSpec  *hyperv1.AzureKMSSpec
	kmsImage string
}

func NewAzureKMSProvider(kmsSpec *hyperv1.AzureKMSSpec, image string) (*azureKMSProvider, error) {
	if kmsSpec == nil {
		return nil, fmt.Errorf("azure kms metadata not specified")
	}
	return &azureKMSProvider{
		kmsSpec:  kmsSpec,
		kmsImage: image,
	}, nil
}

func (p *azureKMSProvider) GenerateKMSEncryptionConfig(apiVersion string) (*v1.EncryptionConfiguration, error) {
	var providerConfiguration []v1.ProviderConfiguration

	activeKeyHash, err := util.HashStruct(p.kmsSpec.ActiveKey)
	if err != nil {
		return nil, err
	}
	providerConfiguration = append(providerConfiguration, v1.ProviderConfiguration{
		KMS: &v1.KMSConfiguration{
			Name:       fmt.Sprintf("%s-%s", azureProviderConfigNamePrefix, activeKeyHash),
			APIVersion: apiVersion,
			Endpoint:   azureActiveKMSUnixSocket,
			Timeout:    &metav1.Duration{Duration: 35 * time.Second},
		},
	})
	if p.kmsSpec.BackupKey != nil {
		backupKeyHash, err := util.HashStruct(p.kmsSpec.BackupKey)
		if err != nil {
			return nil, err
		}
		providerConfiguration = append(providerConfiguration, v1.ProviderConfiguration{
			KMS: &v1.KMSConfiguration{
				Name:       fmt.Sprintf("%s-%s", azureProviderConfigNamePrefix, backupKeyHash),
				APIVersion: apiVersion,
				Endpoint:   azureBackupKMSUnixSocket,
				Timeout:    &metav1.Duration{Duration: 35 * time.Second},
			},
		})
	}

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

func (p *azureKMSProvider) GenerateKMSPodConfig() (*KMSPodConfig, error) {
	podConfig := &KMSPodConfig{}

	podConfig.Volumes = append(podConfig.Volumes,
		util.BuildVolume(kasVolumeAzureKMSCredentials(), buildVolumeAzureKMSCredentials),
		util.BuildVolume(kasVolumeKMSSocket(), buildVolumeKMSSocket),
		util.BuildVolume(kasVolumeKMSSecretStore(), buildVolumeKMSSecretStore),
		util.BuildVolume(kasVolumeKMSEncryptionClusterSeed(), buildVolumeKMSEncryptionClusterSeed),
	)
	podConfig.Containers = append(podConfig.Containers,
		util.BuildContainer(
			kasContainerAzureKMSActive(),
			p.buildKASContainerAzureKMS(p.kmsSpec.ActiveKey, azureActiveKMSUnixSocket, azureActiveKMSHealthPort, azureActiveKMSMetricsAddr)),
	)
	if p.kmsSpec.BackupKey != nil {
		podConfig.Containers = append(podConfig.Containers,
			util.BuildContainer(
				kasContainerAzureKMSBackup(),
				p.buildKASContainerAzureKMS(*p.kmsSpec.BackupKey, azureBackupKMSUnixSocket, azureBackupKMSHealthPort, azureBackupKMSMetricsAddr)),
		)
	}

	podConfig.KASContainerMutate = func(c *corev1.Container) {
		c.VolumeMounts = append(c.VolumeMounts, azureKMSVolumeMounts.ContainerMounts(KasMainContainerName)...)
	}
	return podConfig, nil
}

func (p *azureKMSProvider) buildKASContainerAzureKMS(kmsKey hyperv1.AzureKMSKey, unixSocketPath string, healthPort int, metricsAddr string) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = p.kmsImage
		c.ImagePullPolicy = corev1.PullIfNotPresent
		c.Ports = []corev1.ContainerPort{
			{
				Name:          "http",
				ContainerPort: int32(healthPort),
				Protocol:      corev1.ProtocolTCP,
			},
		}

		c.Args = []string{
			fmt.Sprintf("--keyvault-name=%s", kmsKey.KeyVaultName),
			fmt.Sprintf("--key-name=%s", kmsKey.KeyName),
			fmt.Sprintf("--key-version=%s", kmsKey.KeyVersion),
			fmt.Sprintf("--listen-addr=%s", unixSocketPath),
			fmt.Sprintf("--healthz-port=%d", healthPort),
			fmt.Sprintf("--metrics-addr=%s", metricsAddr),
			fmt.Sprintf("--encrypted-cluster-seed-file=%s", encryptedClusterSeedLocation),
			"--healthz-path=/healthz",
			fmt.Sprintf("--config-file-path=%s/%s", azureKMSVolumeMounts.Path(c.Name, kasVolumeAzureKMSCredentials().Name), azureKMSCredsFileKey),
			"-v=6",
		}
		c.VolumeMounts = azureKMSVolumeMounts.ContainerMounts(c.Name)
		c.VolumeMounts = append(c.VolumeMounts,
			corev1.VolumeMount{
				Name:      config.ManagedAzureKMSSecretStoreVolumeName,
				MountPath: config.ManagedAzureCertificateMountPath,
				ReadOnly:  true,
			})
		c.LivenessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Scheme: corev1.URISchemeHTTP,
					Port:   intstr.FromInt(healthPort),
					Path:   "/healthz",
				},
			},
			InitialDelaySeconds: 120,
			PeriodSeconds:       300,
			TimeoutSeconds:      160,
			FailureThreshold:    3,
			SuccessThreshold:    1,
		}
		c.Resources = corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceMemory: resource.MustParse("10Mi"),
				corev1.ResourceCPU:    resource.MustParse("10m"),
			},
		}
	}
}

func kasContainerAzureKMSActive() *corev1.Container {
	return &corev1.Container{
		Name: "azure-kms-provider-active",
	}
}

func kasContainerAzureKMSBackup() *corev1.Container {
	return &corev1.Container{
		Name: "azure-kms-provider-backup",
	}
}

func kasVolumeAzureKMSCredentials() *corev1.Volume {
	return &corev1.Volume{
		Name: "azure-kms-credentials",
	}
}

func buildVolumeAzureKMSCredentials(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: manifests.AzureKMSWithCredentials("").Name,
		Items: []corev1.KeyToPath{
			{
				Key:  azure.CloudConfigKey,
				Path: azureKMSCredsFileKey,
			},
		},
	}
}

func kasVolumeKMSSecretStore() *corev1.Volume {
	return &corev1.Volume{
		Name: config.ManagedAzureKMSSecretStoreVolumeName,
	}
}

func buildVolumeKMSSecretStore(v *corev1.Volume) {
	v.VolumeSource = corev1.VolumeSource{
		CSI: &corev1.CSIVolumeSource{
			Driver:   config.ManagedAzureSecretsStoreCSIDriver,
			ReadOnly: ptr.To(true),
			VolumeAttributes: map[string]string{
				config.ManagedAzureSecretProviderClass: config.ManagedAzureKMSSecretProviderClassName,
			},
		},
	}
}

func kasVolumeKMSEncryptionClusterSeed() *corev1.Volume {
	return &corev1.Volume{
		Name: "data",
	}
}

func AdaptAzureSecretProvider(cpContext component.WorkloadContext, secretProvider *secretsstorev1.SecretProviderClass) error {
	managedIdentity := cpContext.HCP.Spec.SecretEncryption.KMS.Azure.KMS
	secretproviderclass.ReconcileManagedAzureSecretProviderClass(secretProvider, cpContext.HCP, managedIdentity, true)
	return nil
}

// AdaptAzureClusterSeedSecretProvider configures the SecretProviderClass for the KMS cluster seed secret.
// This uses a cluster-specific secret name in Azure Key Vault.
func AdaptAzureClusterSeedSecretProvider(cpContext component.WorkloadContext, secretProvider *secretsstorev1.SecretProviderClass) error {
	// Generate cluster-specific secret name using cluster name
	clusterSeedSecretName := fmt.Sprintf("cluster-seed-%s", cpContext.HCP.Name)
	secretproviderclass.ReconcileAzureKMSClusterSeedSecretProviderClass(secretProvider, cpContext.HCP, clusterSeedSecretName)
	return nil
}
