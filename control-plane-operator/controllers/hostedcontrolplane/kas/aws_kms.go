package kas

import (
	"bytes"
	"fmt"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/api"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/util"
	"hash/fnv"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apiserver/pkg/apis/config/v1"
	"k8s.io/utils/pointer"
	"time"
)

const (
	activeAWSKMSUnixSocket         = "unix:///tmp/awskmsactive.sock"
	activeAWSKMSUnixSocketFilePath = "/tmp/awskmsactive.sock"
	activeAWSKMSHealthPort         = 8080
	backupAWSKMSUnixSocket         = "unix:///tmp/awskmsbackup.sock"
	backupAWSKMSUnixSocketFilePath = "/tmp/awskmsbackup.sock"
	backupAWSKMSHealthPort         = 8081
	awsKeyNamePrefix               = "awskmskey"
)

var (
	awsKMSVolumeMounts = util.PodVolumeMounts{
		kasContainerMain().Name: {
			kasVolumeKMSSocket().Name: "/tmp",
		},
		kasContainerAWSKMSActive().Name: {
			kasVolumeKMSSocket().Name:         "/tmp",
			kasVolumeAWSKMSCredentials().Name: "/.aws",
		},
		kasContainerAWSKMSBackup().Name: {
			kasVolumeKMSSocket().Name:         "/tmp",
			kasVolumeAWSKMSCredentials().Name: "/.aws",
		},
	}
)

func generateAWSKMSEncryptionConfig(activeKey *hyperv1.AWSKMSKeyEntry, backupKey *hyperv1.AWSKMSKeyEntry) ([]byte, error) {
	var providerConfiguration []v1.ProviderConfiguration
	if activeKey == nil || len(activeKey.ARN) == 0 {
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

func applyAWSKMSConfig(podSpec *corev1.PodSpec, activeKey *hyperv1.AWSKMSKeyEntry, backupKey *hyperv1.AWSKMSKeyEntry, awsAuth *hyperv1.AWSKMSAuthSpec, awsRegion string, kmsImage string) error {
	if activeKey == nil || len(activeKey.ARN) == 0 || awsAuth == nil || len(kmsImage) == 0 {
		return fmt.Errorf("aws kms active key metadata is nil")
	}
	podSpec.Containers = append(podSpec.Containers, util.BuildContainer(kasContainerAWSKMSActive(), buildKASContainerAWSKMS(kmsImage, activeKey.ARN, awsRegion, activeAWSKMSUnixSocketFilePath, activeAWSKMSHealthPort)))
	if backupKey != nil && len(backupKey.ARN) > 0 {
		podSpec.Containers = append(podSpec.Containers, util.BuildContainer(kasContainerAWSKMSBackup(), buildKASContainerAWSKMS(kmsImage, activeKey.ARN, awsRegion, backupAWSKMSUnixSocketFilePath, backupAWSKMSHealthPort)))
	}
	switch awsAuth.Type {
	case hyperv1.AWSKMSUnmanagedAuth:
		if awsAuth.Unmanaged == nil || len(awsAuth.Unmanaged.Credentials.Name) == 0 {
			return fmt.Errorf("aws kms credential data not specified")
		}
		podSpec.Volumes = append(podSpec.Volumes, util.BuildVolume(kasVolumeAWSKMSCredentials(), buildVolumeAWSKMSCredentials(awsAuth.Unmanaged.Credentials.Name)), util.BuildVolume(kasVolumeKMSSocket(), buildVolumeKMSSocket))
	default:
		return fmt.Errorf("unrecognized aws kms auth type %s", awsAuth.Type)
	}
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

func buildKASContainerAWSKMS(image string, arn string, region string, unixSocketPath string, healthPort int32) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = image
		c.ImagePullPolicy = corev1.PullAlways
		c.Ports = []corev1.ContainerPort{
			{
				Name:          "http",
				ContainerPort: healthPort,
				Protocol:      corev1.ProtocolTCP,
			},
		}
		c.Command = []string{
			"/aws-encryption-provider",
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
