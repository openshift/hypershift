package kms

import (
	"fmt"
	"path"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/azure"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/podspec"
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
)

// AzureKMSProviderName computes the EncryptionConfiguration KMS provider name for an Azure KMS key.
func AzureKMSProviderName(key hyperv1.AzureKMSKey) (string, error) {
	h, err := util.HashStruct(key)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s-%s", azureProviderConfigNamePrefix, h), nil
}

var (
	azureKMSVolumeMounts = podspec.VolumeMounts{
		KasMainContainerName: {
			kasVolumeKMSSocket().Name: "/opt",
		},
		kasContainerAzureKMSActive().Name: {
			kasVolumeKMSSocket().Name:           "/opt",
			kasVolumeAzureKMSCredentials().Name: "/etc/kubernetes",
		},
		kasContainerAzureKMSBackup().Name: {
			kasVolumeKMSSocket().Name:           "/opt",
			kasVolumeAzureKMSCredentials().Name: "/etc/kubernetes",
		},
	}

	azureActiveKMSUnixSocket = fmt.Sprintf("unix://%s/%s", azureKMSVolumeMounts.Path(KasMainContainerName, kasVolumeKMSSocket().Name), azureActiveKMSUnixSocketFileName)
	azureBackupKMSUnixSocket = fmt.Sprintf("unix://%s/%s", azureKMSVolumeMounts.Path(KasMainContainerName, kasVolumeKMSSocket().Name), azureBackupKMSUnixSocketFileName)
)

var _ KMSProvider = &azureKMSProvider{}

type azureKMSProvider struct {
	writeKey         hyperv1.AzureKMSKey
	readKey          *hyperv1.AzureKMSKey
	kmsSpec          *hyperv1.AzureKMSSpec
	kmsImage         string
	isSelfManaged    bool
	kmsClientID      string
	tenantID         string
	tokenMinterImage string
}

// AzureKMSProviderOptions contains optional configuration for Azure KMS providers.
type AzureKMSProviderOptions struct {
	IsSelfManaged    bool
	KMSClientID      string
	TenantID         string
	TokenMinterImage string
}

func NewAzureKMSProvider(writeKey hyperv1.AzureKMSKey, readKey *hyperv1.AzureKMSKey, kmsSpec *hyperv1.AzureKMSSpec, image string, opts AzureKMSProviderOptions) (*azureKMSProvider, error) {
	if kmsSpec == nil {
		return nil, fmt.Errorf("azure kms metadata not specified")
	}
	if opts.IsSelfManaged {
		if opts.KMSClientID == "" || opts.TenantID == "" {
			return nil, fmt.Errorf("kmsClientID and tenantID are required for self-managed Azure KMS")
		}
		if opts.TokenMinterImage == "" {
			return nil, fmt.Errorf("tokenMinterImage is required for self-managed Azure KMS")
		}
	}
	return &azureKMSProvider{
		writeKey:         writeKey,
		readKey:          readKey,
		kmsSpec:          kmsSpec,
		kmsImage:         image,
		isSelfManaged:    opts.IsSelfManaged,
		kmsClientID:      opts.KMSClientID,
		tenantID:         opts.TenantID,
		tokenMinterImage: opts.TokenMinterImage,
	}, nil
}

func (p *azureKMSProvider) GenerateKMSEncryptionConfig(apiVersion string) (*v1.EncryptionConfiguration, error) {
	var providerConfiguration []v1.ProviderConfiguration

	writeKeyName, err := AzureKMSProviderName(p.writeKey)
	if err != nil {
		return nil, err
	}
	providerConfiguration = append(providerConfiguration, v1.ProviderConfiguration{
		KMS: &v1.KMSConfiguration{
			Name:       writeKeyName,
			APIVersion: apiVersion,
			Endpoint:   azureActiveKMSUnixSocket,
			Timeout:    &metav1.Duration{Duration: 35 * time.Second},
		},
	})
	if p.readKey != nil {
		readKeyName, err := AzureKMSProviderName(*p.readKey)
		if err != nil {
			return nil, err
		}
		providerConfiguration = append(providerConfiguration, v1.ProviderConfiguration{
			KMS: &v1.KMSConfiguration{
				Name:       readKeyName,
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
		podspec.BuildVolume(kasVolumeAzureKMSCredentials(), buildVolumeAzureKMSCredentials),
		podspec.BuildVolume(kasVolumeKMSSocket(), buildVolumeKMSSocket),
	)

	if p.isSelfManaged {
		podConfig.Volumes = append(podConfig.Volumes,
			podspec.BuildVolume(kasVolumeAzureKMSCloudToken(), buildVolumeAzureKMSCloudToken),
		)
	} else {
		podConfig.Volumes = append(podConfig.Volumes,
			podspec.BuildVolume(kasVolumeKMSSecretStore(), buildVolumeKMSSecretStore),
		)
	}

	podConfig.Containers = append(podConfig.Containers,
		podspec.BuildContainer(
			kasContainerAzureKMSActive(),
			p.buildKASContainerAzureKMS(p.writeKey, azureActiveKMSUnixSocket, azureActiveKMSHealthPort, azureActiveKMSMetricsAddr)),
	)
	if p.readKey != nil {
		podConfig.Containers = append(podConfig.Containers,
			podspec.BuildContainer(
				kasContainerAzureKMSBackup(),
				p.buildKASContainerAzureKMS(*p.readKey, azureBackupKMSUnixSocket, azureBackupKMSHealthPort, azureBackupKMSMetricsAddr)),
		)
	}

	if p.isSelfManaged {
		podConfig.Containers = append(podConfig.Containers,
			podspec.BuildContainer(kasContainerAzureKMSTokenMinter(), p.buildKASContainerAzureKMSTokenMinter()),
		)
	}

	if p.isSelfManaged {
		podConfig.Containers = append(podConfig.Containers,
			podspec.BuildContainer(kasContainerAzureKMSTokenMinter(), p.buildKASContainerAzureKMSTokenMinter()),
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
			"--healthz-path=/healthz",
			fmt.Sprintf("--config-file-path=%s/%s", azureKMSVolumeMounts.Path(c.Name, kasVolumeAzureKMSCredentials().Name), azureKMSCredsFileKey),
			"-v=1",
		}
		c.VolumeMounts = azureKMSVolumeMounts.ContainerMounts(c.Name)

		if p.isSelfManaged {
			c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{
				Name:      kasVolumeAzureKMSCloudToken().Name,
				MountPath: config.CloudTokenMountPath,
				ReadOnly:  true,
			})
			c.Env = append(c.Env,
				corev1.EnvVar{Name: "AZURE_CLIENT_ID", Value: p.kmsClientID},
				corev1.EnvVar{Name: "AZURE_TENANT_ID", Value: p.tenantID},
				corev1.EnvVar{Name: "AZURE_FEDERATED_TOKEN_FILE", Value: path.Join(config.CloudTokenMountPath, "token")},
			)
		} else {
			c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{
				Name:      config.ManagedAzureKMSSecretStoreVolumeName,
				MountPath: config.ManagedAzureCertificateMountPath,
				ReadOnly:  true,
			})
		}

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

func (p *azureKMSProvider) buildKASContainerAzureKMSTokenMinter() func(*corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = p.tokenMinterImage
		c.ImagePullPolicy = corev1.PullIfNotPresent
		c.Command = []string{"/usr/bin/control-plane-operator", "token-minter"}
		c.Args = []string{
			"--token-audience=openshift",
			fmt.Sprintf("--service-account-namespace=%s", manifests.KASContainerKMSProviderServiceAccount().Namespace),
			fmt.Sprintf("--service-account-name=%s", manifests.KASContainerKMSProviderServiceAccount().Name),
			fmt.Sprintf("--token-file=%s", path.Join(config.CloudTokenMountPath, "token")),
			fmt.Sprintf("--kubeconfig=%s", path.Join("/etc/kubernetes", podspec.KubeconfigKey)),
		}
		c.Resources = corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10m"),
				corev1.ResourceMemory: resource.MustParse("30Mi"),
			},
		}
		c.VolumeMounts = []corev1.VolumeMount{
			{
				Name:      kasVolumeAzureKMSCloudToken().Name,
				MountPath: config.CloudTokenMountPath,
			},
			{
				Name:      kasVolumeLocalhostKubeconfig,
				MountPath: "/etc/kubernetes",
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

func kasContainerAzureKMSTokenMinter() *corev1.Container {
	return &corev1.Container{
		Name: "azure-kms-token-minter",
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

func kasVolumeAzureKMSCloudToken() *corev1.Volume {
	return &corev1.Volume{
		Name: "azure-kms-cloud-token",
	}
}

func buildVolumeAzureKMSCloudToken(v *corev1.Volume) {
	v.EmptyDir = &corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumMemory}
}

func AdaptAzureSecretProvider(cpContext component.WorkloadContext, secretProvider *secretsstorev1.SecretProviderClass) error {
	managedIdentity := cpContext.HCP.Spec.SecretEncryption.KMS.Azure.KMS
	if managedIdentity.CredentialsSecretName == "" {
		return fmt.Errorf("managed identity credentials secret name is required for Azure KMS secret provider")
	}
	secretproviderclass.ReconcileManagedAzureSecretProviderClass(secretProvider, cpContext.HCP, managedIdentity)
	return nil
}
