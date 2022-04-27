package kas

import (
	"bytes"
	"fmt"
	"hash/fnv"
	"path"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/support/api"
	"github.com/openshift/hypershift/support/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apiserver/pkg/apis/config/v1"
	"k8s.io/utils/pointer"
)

const (
	activeAWSKMSUnixSocketFileName = "awskmsactive.sock"
	activeAWSKMSHealthPort         = 8080
	backupAWSKMSUnixSocketFileName = "awskmsbackup.sock"
	backupAWSKMSHealthPort         = 8081
	awsKeyNamePrefix               = "awskmskey"
)

var (
	awsKMSVolumeMounts = util.PodVolumeMounts{
		kasContainerMain().Name: {
			kasVolumeKMSSocket().Name: "/var/run",
		},
		kasContainerAWSKMSActive().Name: {
			kasVolumeKMSSocket().Name:                "/var/run",
			kasVolumeAWSKMSCredentials().Name:        "/aws",
			kasVolumeAWSKMSCloudProviderToken().Name: "/var/run/secrets/openshift/serviceaccount",
		},
		kasContainerAWSKMSBackup().Name: {
			kasVolumeKMSSocket().Name:                "/var/run",
			kasVolumeAWSKMSCredentials().Name:        "/aws",
			kasVolumeAWSKMSCloudProviderToken().Name: "/var/run/secrets/openshift/serviceaccount",
		},
		kasContainerAWSKMSTokenMinter().Name: {
			kasVolumeLocalhostKubeconfig().Name:      "/var/secrets/localhost-kubeconfig",
			kasVolumeAWSKMSCloudProviderToken().Name: "/var/run/secrets/openshift/serviceaccount",
		},
	}

	backupAWSKMSUnixSocket = fmt.Sprintf("unix://%s/%s", awsKMSVolumeMounts.Path(kasContainerMain().Name, kasVolumeKMSSocket().Name), backupAWSKMSUnixSocketFileName)
	activeAWSKMSUnixSocket = fmt.Sprintf("unix://%s/%s", awsKMSVolumeMounts.Path(kasContainerMain().Name, kasVolumeKMSSocket().Name), activeAWSKMSUnixSocketFileName)
)

func generateAWSKMSEncryptionConfig(activeKey hyperv1.AWSKMSKeyEntry, backupKey *hyperv1.AWSKMSKeyEntry) ([]byte, error) {
	var providerConfiguration []v1.ProviderConfiguration
	if len(activeKey.ARN) == 0 {
		return nil, fmt.Errorf("active key metadata is nil")
	}
	hasher := fnv.New32()
	_, err := hasher.Write([]byte(activeKey.ARN))
	if err != nil {
		return nil, err
	}
	providerConfiguration = append(providerConfiguration, v1.ProviderConfiguration{
		KMS: &v1.KMSConfiguration{
			Name:      fmt.Sprintf("%s-%d", awsKeyNamePrefix, hasher.Sum32()),
			Endpoint:  activeAWSKMSUnixSocket,
			CacheSize: pointer.Int32Ptr(100),
			Timeout:   &metav1.Duration{Duration: 35 * time.Second},
		},
	})
	if backupKey != nil && len(backupKey.ARN) > 0 {
		hasher = fnv.New32()
		_, err := hasher.Write([]byte(backupKey.ARN))
		if err != nil {
			return nil, err
		}
		providerConfiguration = append(providerConfiguration, v1.ProviderConfiguration{
			KMS: &v1.KMSConfiguration{
				Name:      fmt.Sprintf("%s-%d", awsKeyNamePrefix, hasher.Sum32()),
				Endpoint:  backupAWSKMSUnixSocket,
				CacheSize: pointer.Int32Ptr(100),
				Timeout:   &metav1.Duration{Duration: 35 * time.Second},
			},
		})
	}
	providerConfiguration = append(providerConfiguration, v1.ProviderConfiguration{
		Identity: &v1.IdentityConfiguration{},
	})
	encryptionConfig := v1.EncryptionConfiguration{
		TypeMeta: metav1.TypeMeta{
			APIVersion: v1.SchemeGroupVersion.String(),
			Kind:       encryptionConfigurationKind,
		},
		Resources: []v1.ResourceConfiguration{
			{
				Resources: []string{"secrets"},
				Providers: providerConfiguration,
			},
		},
	}
	bufferInstance := bytes.NewBuffer([]byte{})
	err = api.YamlSerializer.Encode(&encryptionConfig, bufferInstance)
	if err != nil {
		return nil, err
	}
	return bufferInstance.Bytes(), nil
}

func applyAWSKMSConfig(podSpec *corev1.PodSpec, activeKey hyperv1.AWSKMSKeyEntry, backupKey *hyperv1.AWSKMSKeyEntry, awsAuth hyperv1.AWSKMSAuthSpec, awsRegion string, kmsImage, tokenMinterImage string) error {
	if len(activeKey.ARN) == 0 || len(kmsImage) == 0 {
		return fmt.Errorf("aws kms active key metadata is nil")
	}
	podSpec.Containers = append(podSpec.Containers, util.BuildContainer(kasContainerAWSKMSTokenMinter(), buildKASContainerAWSKMSTokenMinter(tokenMinterImage)))
	podSpec.Containers = append(podSpec.Containers, util.BuildContainer(kasContainerAWSKMSActive(), buildKASContainerAWSKMS(kmsImage, activeKey.ARN, awsRegion, fmt.Sprintf("%s/%s", awsKMSVolumeMounts.Path(kasContainerMain().Name, kasVolumeKMSSocket().Name), activeAWSKMSUnixSocketFileName), activeAWSKMSHealthPort)))
	if backupKey != nil && len(backupKey.ARN) > 0 {
		podSpec.Containers = append(podSpec.Containers, util.BuildContainer(kasContainerAWSKMSBackup(), buildKASContainerAWSKMS(kmsImage, activeKey.ARN, awsRegion, fmt.Sprintf("%s/%s", awsKMSVolumeMounts.Path(kasContainerMain().Name, kasVolumeKMSSocket().Name), backupAWSKMSUnixSocketFileName), backupAWSKMSHealthPort)))
	}
	if len(awsAuth.Credentials.Name) == 0 {
		return fmt.Errorf("aws kms credential data not specified")
	}
	podSpec.Volumes = append(podSpec.Volumes,
		util.BuildVolume(kasVolumeAWSKMSCredentials(), buildVolumeAWSKMSCredentials(awsAuth.Credentials.Name)),
		util.BuildVolume(kasVolumeKMSSocket(), buildVolumeKMSSocket),
		util.BuildVolume(kasVolumeAWSKMSCloudProviderToken(), buildKASVolumeAWSKMSCloudProviderToken),
	)
	var container *corev1.Container
	for i, c := range podSpec.Containers {
		if c.Name == kasContainerMain().Name {
			container = &podSpec.Containers[i]
			break
		}
	}
	if container == nil {
		panic("main kube apiserver container not found in spec")
	}
	container.VolumeMounts = append(container.VolumeMounts,
		awsKMSVolumeMounts.ContainerMounts(kasContainerMain().Name)...)
	return nil
}

func kasContainerAWSKMSActive() *corev1.Container {
	return &corev1.Container{
		Name: "aws-kms-active",
	}
}

func kasContainerAWSKMSBackup() *corev1.Container {
	return &corev1.Container{
		Name: "aws-kms-backup",
	}
}

func kasContainerAWSKMSTokenMinter() *corev1.Container {
	return &corev1.Container{
		Name: "aws-kms-token-minter",
	}
}

func buildKASContainerAWSKMS(image string, arn string, region string, unixSocketPath string, healthPort int32) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = image
		c.ImagePullPolicy = corev1.PullIfNotPresent
		c.Ports = []corev1.ContainerPort{
			{
				Name:          "http",
				ContainerPort: healthPort,
				Protocol:      corev1.ProtocolTCP,
			},
		}
		c.Env = append(c.Env,
			corev1.EnvVar{
				Name:  "AWS_SHARED_CREDENTIALS_FILE",
				Value: path.Join(awsKMSVolumeMounts.Path(c.Name, kasVolumeAWSKMSCredentials().Name), hyperv1.AWSCredentialsFileSecretKey),
			},
			corev1.EnvVar{
				Name:  "AWS_SDK_LOAD_CONFIG",
				Value: "true",
			},
			corev1.EnvVar{
				Name:  "AWS_EC2_METADATA_DISABLED",
				Value: "true",
			})
		c.Args = []string{
			fmt.Sprintf("--key=%s", arn),
			fmt.Sprintf("--region=%s", region),
			fmt.Sprintf("--listen=%s", unixSocketPath),
			fmt.Sprintf("--health-port=:%d", healthPort),
		}
		c.VolumeMounts = awsKMSVolumeMounts.ContainerMounts(c.Name)
	}
}

func kasVolumeAWSKMSCredentials() *corev1.Volume {
	return &corev1.Volume{
		Name: "aws-kms-credentials",
	}
}

func buildVolumeAWSKMSCredentials(secretName string) func(*corev1.Volume) {
	return func(v *corev1.Volume) {
		v.Secret = &corev1.SecretVolumeSource{}
		v.Secret.SecretName = secretName
	}
}

func buildKASContainerAWSKMSTokenMinter(image string) func(*corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = image
		c.ImagePullPolicy = corev1.PullIfNotPresent
		c.Command = []string{"/usr/bin/control-plane-operator", "token-minter"}
		c.Args = []string{
			"--service-account-namespace=kube-system",
			"--service-account-name=kms-provider",
			"--token-audience=openshift",
			fmt.Sprintf("--token-file=%s", path.Join(awsKMSVolumeMounts.Path(c.Name, kasVolumeAWSKMSCloudProviderToken().Name), "token")),
			fmt.Sprintf("--kubeconfig=%s", path.Join(awsKMSVolumeMounts.Path(c.Name, kasVolumeLocalhostKubeconfig().Name), KubeconfigKey)),
		}
		c.VolumeMounts = awsKMSVolumeMounts.ContainerMounts(c.Name)
	}
}

func kasVolumeAWSKMSCloudProviderToken() *corev1.Volume {
	return &corev1.Volume{
		Name: "aws-kms-token",
	}
}

func buildKASVolumeAWSKMSCloudProviderToken(v *corev1.Volume) {
	v.EmptyDir = &corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumMemory}
}
