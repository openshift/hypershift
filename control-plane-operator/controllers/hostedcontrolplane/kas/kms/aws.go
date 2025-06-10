package kms

import (
	"fmt"
	"hash/fnv"
	"path"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/cloud/aws"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/support/config"
	"github.com/openshift/hypershift/support/util"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	v1 "k8s.io/apiserver/pkg/apis/apiserver/v1"
	"k8s.io/utils/ptr"
)

const (
	activeAWSKMSUnixSocketFileName = "awskmsactive.sock"
	activeAWSKMSHealthPort         = 8080
	backupAWSKMSUnixSocketFileName = "awskmsbackup.sock"
	backupAWSKMSHealthPort         = 8081
	awsKeyNamePrefix               = "awskmskey"
	kmsAPIVersionV1                = "v1"
)

var (
	awsKMSVolumeMounts = util.PodVolumeMounts{
		KasMainContainerName: {
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
			kasVolumeLocalhostKubeconfig:             "/var/secrets/localhost-kubeconfig",
			kasVolumeAWSKMSCloudProviderToken().Name: "/var/run/secrets/openshift/serviceaccount",
		},
	}

	backupAWSKMSUnixSocket = fmt.Sprintf("unix://%s/%s", awsKMSVolumeMounts.Path(KasMainContainerName, kasVolumeKMSSocket().Name), backupAWSKMSUnixSocketFileName)
	activeAWSKMSUnixSocket = fmt.Sprintf("unix://%s/%s", awsKMSVolumeMounts.Path(KasMainContainerName, kasVolumeKMSSocket().Name), activeAWSKMSUnixSocketFileName)
)

var _ IKMSProvider = &awsKMSProvider{}

type awsKMSProvider struct {
	activeKey        hyperv1.AWSKMSKeyEntry
	backupKey        *hyperv1.AWSKMSKeyEntry
	awsAuth          hyperv1.AWSKMSAuthSpec
	awsRegion        string
	kmsImage         string
	tokenMinterImage string
}

func NewAWSKMSProvider(kmsSpec *hyperv1.AWSKMSSpec, kmsImage, tokenMinterImage string) (*awsKMSProvider, error) {
	if kmsSpec == nil {
		return nil, fmt.Errorf("AWS kms metadata not specified")
	}
	return &awsKMSProvider{
		activeKey:        kmsSpec.ActiveKey,
		backupKey:        kmsSpec.BackupKey,
		awsAuth:          kmsSpec.Auth,
		awsRegion:        kmsSpec.Region,
		kmsImage:         kmsImage,
		tokenMinterImage: tokenMinterImage,
	}, nil
}

func (p *awsKMSProvider) GenerateKMSEncryptionConfig(apiVersion string) (*v1.EncryptionConfiguration, error) {
	var providerConfiguration []v1.ProviderConfiguration
	if len(p.activeKey.ARN) == 0 {
		return nil, fmt.Errorf("active key metadata is nil")
	}
	hasher := fnv.New32()
	_, err := hasher.Write([]byte(p.activeKey.ARN))
	if err != nil {
		return nil, err
	}
	providerConfiguration = append(providerConfiguration, v1.ProviderConfiguration{
		KMS: &v1.KMSConfiguration{
			APIVersion: apiVersion,
			Name:       fmt.Sprintf("%s-%d", awsKeyNamePrefix, hasher.Sum32()),
			Endpoint:   activeAWSKMSUnixSocket,
			Timeout:    &metav1.Duration{Duration: 35 * time.Second},
		},
	})
	if p.backupKey != nil && len(p.backupKey.ARN) > 0 {
		hasher = fnv.New32()
		_, err := hasher.Write([]byte(p.backupKey.ARN))
		if err != nil {
			return nil, err
		}
		providerConfiguration = append(providerConfiguration, v1.ProviderConfiguration{
			KMS: &v1.KMSConfiguration{
				APIVersion: apiVersion,
				Name:       fmt.Sprintf("%s-%d", awsKeyNamePrefix, hasher.Sum32()),
				Endpoint:   backupAWSKMSUnixSocket,
				Timeout:    &metav1.Duration{Duration: 35 * time.Second},
			},
		})
	}

	if apiVersion == kmsAPIVersionV1 {
		for _, p := range providerConfiguration {
			p.KMS.CacheSize = ptr.To[int32](100)
		}
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

func (p *awsKMSProvider) ApplyKMSConfig(podSpec *corev1.PodSpec) error {
	if len(p.activeKey.ARN) == 0 || len(p.kmsImage) == 0 {
		return fmt.Errorf("aws kms active key metadata is nil")
	}
	podSpec.Containers = append(podSpec.Containers, util.BuildContainer(kasContainerAWSKMSTokenMinter(), buildKASContainerAWSKMSTokenMinter(p.tokenMinterImage)))
	podSpec.Containers = append(podSpec.Containers, util.BuildContainer(kasContainerAWSKMSActive(), buildKASContainerAWSKMS(p.kmsImage, p.activeKey.ARN, p.awsRegion, fmt.Sprintf("%s/%s", awsKMSVolumeMounts.Path(KasMainContainerName, kasVolumeKMSSocket().Name), activeAWSKMSUnixSocketFileName), activeAWSKMSHealthPort)))
	if p.backupKey != nil && len(p.backupKey.ARN) > 0 {
		podSpec.Containers = append(podSpec.Containers, util.BuildContainer(kasContainerAWSKMSBackup(), buildKASContainerAWSKMS(p.kmsImage, p.backupKey.ARN, p.awsRegion, fmt.Sprintf("%s/%s", awsKMSVolumeMounts.Path(KasMainContainerName, kasVolumeKMSSocket().Name), backupAWSKMSUnixSocketFileName), backupAWSKMSHealthPort)))
	}
	if len(p.awsAuth.AWSKMSRoleARN) == 0 {
		return fmt.Errorf("aws kms role arn not specified")
	}
	podSpec.Volumes = append(podSpec.Volumes,
		util.BuildVolume(kasVolumeAWSKMSCredentials(), buildVolumeAWSKMSCredentials(aws.AWSKMSCredsSecret("").Name)),
		util.BuildVolume(kasVolumeKMSSocket(), buildVolumeKMSSocket),
		util.BuildVolume(kasVolumeAWSKMSCloudProviderToken(), buildKASVolumeAWSKMSCloudProviderToken),
	)
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
		awsKMSVolumeMounts.ContainerMounts(KasMainContainerName)...)

	container.Args = append(container.Args, "--encryption-provider-config-automatic-reload=false")

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

func buildKASContainerAWSKMS(image string, arn string, region string, unixSocketPath string, healthPort int) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = image
		c.ImagePullPolicy = corev1.PullIfNotPresent
		c.TerminationMessagePolicy = corev1.TerminationMessageFallbackToLogsOnError
		c.Ports = []corev1.ContainerPort{
			{
				Name:          "http",
				ContainerPort: int32(healthPort),
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
		c.LivenessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Scheme: corev1.URISchemeHTTP,
					Port:   intstr.FromInt(healthPort),
					Path:   "healthz",
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
		c.TerminationMessagePolicy = corev1.TerminationMessageFallbackToLogsOnError
		c.Command = []string{"/usr/bin/control-plane-operator", "token-minter"}
		c.Args = []string{
			"--token-audience=openshift",
			fmt.Sprintf("--service-account-namespace=%s", manifests.KASContainerAWSKMSProviderServiceAccount().Namespace),
			fmt.Sprintf("--service-account-name=%s", manifests.KASContainerAWSKMSProviderServiceAccount().Name),
			fmt.Sprintf("--token-file=%s", path.Join(awsKMSVolumeMounts.Path(c.Name, kasVolumeAWSKMSCloudProviderToken().Name), "token")),
			fmt.Sprintf("--kubeconfig=%s", path.Join(awsKMSVolumeMounts.Path(c.Name, kasVolumeLocalhostKubeconfig), util.KubeconfigKey)),
		}
		c.Resources.Requests = corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("10m"),
			corev1.ResourceMemory: resource.MustParse("30Mi"),
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
