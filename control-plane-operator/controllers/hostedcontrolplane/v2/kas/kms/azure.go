package kms

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/url"
	"path"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/azure"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	hyperazureutil "github.com/openshift/hypershift/support/azureutil"
	"github.com/openshift/hypershift/support/config"
	component "github.com/openshift/hypershift/support/controlplane-component"
	"github.com/openshift/hypershift/support/secretproviderclass"
	"github.com/openshift/hypershift/support/util"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/Azure/azure-sdk-for-go/sdk/security/keyvault/azkeys"
	"github.com/Azure/msi-dataplane/pkg/dataplane"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	v1 "k8s.io/apiserver/pkg/apis/apiserver/v1"
	"k8s.io/apiserver/pkg/storage/value/encrypt/aes"
	"k8s.io/kms/pkg/service"
	"k8s.io/utils/ptr"

	"sigs.k8s.io/controller-runtime/pkg/client"
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

	versionAnnotationKey   = "version.azure.akv.io"
	algorithmAnnotationKey = "algorithm.azure.akv.io"
	// encryptionResponseVersion is validated prior to decryption.
	// This is helpful in case we want to change anything about the data we send in the future.
	encryptionResponseVersion = "1"

	clusterSeedKey = "encrypted-cluster-seed"
)

var (
	azureKMSVolumeMounts = util.PodVolumeMounts{
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
	kmsSpec   *hyperv1.AzureKMSSpec
	kmsImage  string
	namespace string
}

func NewAzureKMSProvider(hcpNamespace string, kmsSpec *hyperv1.AzureKMSSpec, image string) (*azureKMSProvider, error) {
	if kmsSpec == nil {
		return nil, fmt.Errorf("azure kms metadata not specified")
	}
	return &azureKMSProvider{
		namespace: hcpNamespace,
		kmsSpec:   kmsSpec,
		kmsImage:  image,
	}, nil
}

// GenerateKMSEncryptionConfig generates the encryption configuration for the KMS provider
// based on the provided API version and the KMS specification.
func (p *azureKMSProvider) GenerateKMSEncryptionConfig(apiVersion string) (*v1.EncryptionConfiguration, error) {
	var providerConfiguration []v1.ProviderConfiguration

	// Generate the active KMS configuration
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

	// Generate the backup KMS configuration if it exists
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

	// Append the KMS configurations to the encryption configuration
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

// GenerateKMSPodConfig generates the container configuration for the Azure KMS provider
func (p *azureKMSProvider) GenerateKMSPodConfig() (*KMSPodConfig, error) {
	podConfig := &KMSPodConfig{}

	// Setup the volumes for the Azure KMS provider container
	podConfig.Volumes = append(podConfig.Volumes,
		util.BuildVolume(kasVolumeAzureKMSCredentials(), buildVolumeAzureKMSCredentials), // contains formatted Azure KMS MI credentials
		util.BuildVolume(kasVolumeKMSSocket(), buildVolumeKMSSocket),                     // contains the Azure KMS socket for communication with the KMS provider
		util.BuildVolume(kasVolumeKMSSecretStore(), buildVolumeKMSSecretStore),           // contains the Azure KMS MI credentials to authenticate with Azure Cloud from the Azure Key Vault
		util.BuildVolume(kasVolumeAzureKMSClusterSeed(), buildVolumeAzureKMSClusterSeed), // contains the Azure KMS cluster seed for encryption/decryption
	)
	podConfig.Volumes = append(podConfig.Volumes, buildVolumeKMSEncryptionClusterSeed(p.namespace))

	// Setup the Azure KMS provider container for the active key
	podConfig.Containers = append(podConfig.Containers,
		util.BuildContainer(
			kasContainerAzureKMSActive(),
			p.buildKASContainerAzureKMS(p.kmsSpec.ActiveKey, azureActiveKMSUnixSocket, azureActiveKMSHealthPort, azureActiveKMSMetricsAddr)),
	)

	// Setup the Azure KMS provider container for the backup key if it exists
	if p.kmsSpec.BackupKey != nil {
		podConfig.Containers = append(podConfig.Containers,
			util.BuildContainer(
				kasContainerAzureKMSBackup(),
				p.buildKASContainerAzureKMS(*p.kmsSpec.BackupKey, azureBackupKMSUnixSocket, azureBackupKMSHealthPort, azureBackupKMSMetricsAddr)),
		)
	}

	// Adds the volume mounts to the Azure KMS provider container
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
			fmt.Sprintf("--encrypted-cluster-seed-file=%s", path.Join(config.AzureKMSSeedMountPath, "clusterSeed")),
			"--healthz-path=/healthz",
			fmt.Sprintf("--config-file-path=%s/%s", azureKMSVolumeMounts.Path(c.Name, kasVolumeAzureKMSCredentials().Name), azureKMSCredsFileKey),
			"-v=2",
		}
		c.VolumeMounts = azureKMSVolumeMounts.ContainerMounts(c.Name)
		c.VolumeMounts = append(c.VolumeMounts,
			corev1.VolumeMount{
				Name:      config.ManagedAzureKMSSecretStoreVolumeName,
				MountPath: config.ManagedAzureCertificateMountPath,
				ReadOnly:  true,
			},
			corev1.VolumeMount{
				Name:      config.AzureKMSSeedSecretName,
				MountPath: config.AzureKMSSeedMountPath,
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

// kasVolumeAzureKMSClusterSeed returns a volume related to the Azure KMS cluster seed.
func kasVolumeAzureKMSClusterSeed() *corev1.Volume {
	return &corev1.Volume{
		Name: config.AzureKMSSeedSecretName,
	}
}

// buildVolumeAzureKMSClusterSeed builds the volume for the Azure KMS cluster seed. This volume is used to store the
// cluster seed for encryption/decryption and is mounted in the Azure KMS provider container.
func buildVolumeAzureKMSClusterSeed(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{
		SecretName: config.AzureKMSSeedSecretName,
		Items: []corev1.KeyToPath{
			{
				Key:  clusterSeedKey,
				Path: "clusterSeed",
			},
		},
	}
}

// kasVolumeKMSSecretStore returns a volume related to the Azure SecretProviderClass.
func kasVolumeKMSSecretStore() *corev1.Volume {
	return &corev1.Volume{
		Name: config.ManagedAzureKMSSecretStoreVolumeName,
	}
}

// buildVolumeKMSSecretStore builds the volume for the Azure SecretProviderClass.
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

// AdaptAzureSecretProvider reconciles the SecretProviderClass for Azure KMS
func AdaptAzureSecretProvider(cpContext component.WorkloadContext, secretProvider *secretsstorev1.SecretProviderClass) error {
	managedIdentity := cpContext.HCP.Spec.SecretEncryption.KMS.Azure.KMS
	secretproviderclass.ReconcileManagedAzureSecretProviderClass(secretProvider, cpContext.HCP, managedIdentity, true)
	return nil
}

func AdaptAzureKMSSeed(cpContext component.WorkloadContext, secret *corev1.Secret) error {
	// Check if the secret already exists
	azureKMSSeedSecret := manifests.AzureKMSSeed(cpContext.HCP.Namespace)
	if err := cpContext.Client.Get(cpContext, client.ObjectKeyFromObject(azureKMSSeedSecret), azureKMSSeedSecret); err != nil {
		if apierrors.IsNotFound(err) {
			// Create the secret if it doesn't exist
			azureKMSSeedSecret.Data = map[string][]byte{
				azureKMSCredsFileKey: []byte(azureKMSSeedSecret.Name),
			}
			azureKMSSeedSecret.Type = corev1.SecretTypeOpaque
			azureKMSSeedSecret.ObjectMeta = metav1.ObjectMeta{
				Name:      azureKMSSeedSecret.Name,
				Namespace: cpContext.HCP.Namespace,
			}

			// Set up the key vault client
			kvClient, err := getKeyVaultClient(cpContext.HCP)
			if err != nil {
				return fmt.Errorf("failed to create key vault client: %w", err)
			}

			// Generate a new cluster seed
			clusterSeed, err := aes.GenerateKey(sha256.BlockSize) // larger seeds will be hashed down to this size
			if err != nil {
				return fmt.Errorf("failed to generate cluster seed: %w", err)
			}

			//Encrypt the cluster seed
			encryptedClusterSeedResp, err := kvClient.Encrypt(context.Background(), clusterSeed, azkeys.EncryptionAlgorithmRSAOAEP256)
			if err != nil {
				return fmt.Errorf("failed to encrypt cluster seed: %w", err)
			}

			if secret.Data == nil {
				secret.Data = map[string][]byte{}
			}
			secret.Data[clusterSeedKey] = encryptedClusterSeedResp.Ciphertext
			secret.Data["key-id"] = []byte(encryptedClusterSeedResp.KeyID)
			return nil
		}
		return fmt.Errorf("failed to get azure kms seed secret: %w", err)
	}
	return nil
}

// TODO everything below this line was copied/modified from the azure kms provider just to get this POC working
type AzureConfig struct {
	Cloud                       string `json:"cloud" yaml:"cloud"`
	TenantID                    string `json:"tenantId" yaml:"tenantId"`
	ClientID                    string `json:"aadClientId" yaml:"aadClientId"`
	ClientSecret                string `json:"aadClientSecret" yaml:"aadClientSecret"`
	UseManagedIdentityExtension bool   `json:"useManagedIdentityExtension,omitempty" yaml:"useManagedIdentityExtension,omitempty"`
	UserAssignedIdentityID      string `json:"userAssignedIdentityID,omitempty" yaml:"userAssignedIdentityID,omitempty"`
	AADClientCertPath           string `json:"aadClientCertPath" yaml:"aadClientCertPath"`
	AADClientCertPassword       string `json:"aadClientCertPassword" yaml:"aadClientCertPassword"`
	AADMSIDataPlaneIdentityPath string `json:"aadMSIDataPlaneIdentityPath,omitempty" yaml:"aadMSIDataPlaneIdentityPath,omitempty"`
}

type KeyVaultClient struct {
	baseClient *azkeys.Client
	config     *AzureConfig
	vaultName  string
	keyName    string
	keyVersion string
	keyIDHash  string
}

func getKeyVaultClient(hcp *hyperv1.HostedControlPlane) (KeyVaultClient, error) {
	azureKeyVaultDNSSuffix, err := hyperazureutil.GetKeyVaultDNSSuffixFromCloudType(hcp.Spec.Platform.Azure.Cloud)
	if err != nil {
		//TODO
	}

	// Retrieve the KMS UserAssignedCredentials path
	credentialsPath := config.ManagedAzureCredentialsPathForKMS + hcp.Spec.SecretEncryption.KMS.Azure.KMS.CredentialsSecretName
	cred, err := dataplane.NewUserAssignedIdentityCredential(context.Background(), credentialsPath, dataplane.WithClientOpts(azcore.ClientOptions{Cloud: cloud.AzurePublic}))
	if err != nil {
		// TODO
	}

	vaultURL := fmt.Sprintf("https://%s.%s", hcp.Spec.SecretEncryption.KMS.Azure.ActiveKey.KeyVaultName, azureKeyVaultDNSSuffix)
	keysClient, err := azkeys.NewClient(vaultURL, cred, nil)
	if err != nil {
		//TODO
	}

	baseURL, err := url.Parse(vaultURL)
	if err != nil {
		//TODO
	}
	urlPath := path.Join("keys", hcp.Spec.SecretEncryption.KMS.Azure.ActiveKey.KeyName, hcp.Spec.SecretEncryption.KMS.Azure.ActiveKey.KeyVersion)
	keyID := baseURL.ResolveReference(
		&url.URL{
			Path: urlPath,
		},
	).String()

	return KeyVaultClient{
		baseClient: keysClient,
		config: &AzureConfig{
			Cloud:                       hcp.Spec.Platform.Azure.Cloud,
			TenantID:                    hcp.Spec.Platform.Azure.TenantID,
			AADMSIDataPlaneIdentityPath: hcp.Spec.SecretEncryption.KMS.Azure.KMS.CredentialsSecretName,
		},
		vaultName:  hcp.Spec.SecretEncryption.KMS.Azure.ActiveKey.KeyVaultName,
		keyName:    hcp.Spec.SecretEncryption.KMS.Azure.ActiveKey.KeyName,
		keyVersion: hcp.Spec.SecretEncryption.KMS.Azure.ActiveKey.KeyVersion,
		keyIDHash:  fmt.Sprintf("%x", sha256.Sum256([]byte(keyID))),
	}, nil
}

func (kvc *KeyVaultClient) Encrypt(
	ctx context.Context,
	plain []byte,
	encryptionAlgorithm azkeys.EncryptionAlgorithm,
) (*service.EncryptResponse, error) {
	value := base64.RawURLEncoding.EncodeToString(plain)

	params := azkeys.KeyOperationParameters{
		Algorithm: &encryptionAlgorithm,
		Value:     []byte(value),
	}
	result, err := kvc.baseClient.Encrypt(ctx, kvc.keyName, kvc.keyVersion, params, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt, error: %+v", err)
	}

	if kvc.keyIDHash != fmt.Sprintf("%x", sha256.Sum256([]byte(*result.KID))) {
		return nil, fmt.Errorf(
			"key id initialized does not match with the key id from encryption result, expected: %s, got: %s",
			kvc.keyIDHash,
			*result.KID,
		)
	}

	annotations := map[string][]byte{
		// dateAnnotationKey:           []byte(result.Header.Get(dateAnnotationValue)),
		// requestIDAnnotationKey:      []byte(result.Header.Get(requestIDAnnotationValue)),
		// keyvaultRegionAnnotationKey: []byte(result.Header.Get(keyvaultRegionAnnotationValue)),
		versionAnnotationKey:   []byte(encryptionResponseVersion),
		algorithmAnnotationKey: []byte(encryptionAlgorithm),
	}

	return &service.EncryptResponse{
		Ciphertext:  result.Result,
		KeyID:       kvc.keyIDHash,
		Annotations: annotations,
	}, nil
}
