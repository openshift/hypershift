package kms

import (
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
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

var (
	ibmCloudKMSVolumeMounts = util.PodVolumeMounts{
		KasMainContainerName: {
			kasVolumeKMSSocket().Name: "/tmp",
		},
		kasContainerIBMCloudKMS().Name: {
			kasVolumeKMSSocket().Name:                 "/tmp",
			kasVolumeIBMCloudKMSKP().Name:             "/tmp/kp",
			kasVolumeIBMCloudKMSProjectedToken().Name: "/etc/pod-identity-token",
		},
	}
	ibmCloudKMSUnixSocket = fmt.Sprintf("unix://%s/%s", ibmCloudKMSVolumeMounts.Path(KasMainContainerName, kasVolumeKMSSocket().Name), ibmCloudKMSUnixSocketFileName)
)

const (
	podIdentityTokenIdentifier    = "pod-identity-token"
	ibmCloudKMSUnixSocketFileName = "keyprotectprovider.sock"
	ibmCloudKMSWDEKSecretKeyName  = "wdek"
	ibmCloudKMSWDEKStateKeyName   = "state"
	ibmKeyNamePrefix              = "ibm"
	ibmCloudKMSHealthPort         = 8081
)

var _ KMSProvider = &ibmCloudKMSProvider{}

type ibmCloudKMSProvider struct {
	ibmCloud *hyperv1.IBMCloudKMSSpec
	kmsImage string
}

func NewIBMCloudKMSProvider(ibmCloud *hyperv1.IBMCloudKMSSpec, kmsImage string) (*ibmCloudKMSProvider, error) {
	if ibmCloud == nil || len(ibmCloud.KeyList) == 0 || len(ibmCloud.Region) == 0 {
		return nil, fmt.Errorf("ibmcloud kms metadata not specified")
	}
	return &ibmCloudKMSProvider{
		ibmCloud: ibmCloud,
		kmsImage: kmsImage,
	}, nil
}

func (p *ibmCloudKMSProvider) GenerateKMSEncryptionConfig(_ string) (*v1.EncryptionConfiguration, error) {

	providerConfiguration := []v1.ProviderConfiguration{
		{
			KMS: &v1.KMSConfiguration{
				APIVersion: "v2",
				Name:       fmt.Sprintf("%s%s", ibmKeyNamePrefix, "v2"),
				Endpoint:   ibmCloudKMSUnixSocket,
				Timeout:    &metav1.Duration{Duration: 35 * time.Second},
			},
		},
		{
			Identity: &v1.IdentityConfiguration{},
		},
	}

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

type ibmCloudKMSInfoEnvVarEntry struct {
	CRKID            string `json:"crkID"`
	InstanceID       string `json:"instanceID"`
	CorrelationID    string `json:"correlationID"`
	URL              string `json:"url"`
	ServiceToService bool   `json:"serviceToService"`
}

func buildIBMCloudKMSInfoEnvVar(keyVersionKeyEntryMap map[int]hyperv1.IBMCloudKMSKeyEntry, authType hyperv1.IBMCloudKMSAuthType) (string, error) {
	serializeMap := map[string]ibmCloudKMSInfoEnvVarEntry{}
	serviceToService := authType == hyperv1.IBMCloudKMSManagedAuth
	for keyVersion, kmsKeyEntry := range keyVersionKeyEntryMap {
		serializeMap[strconv.Itoa(keyVersion)] = ibmCloudKMSInfoEnvVarEntry{
			CRKID:            kmsKeyEntry.CRKID,
			InstanceID:       kmsKeyEntry.InstanceID,
			CorrelationID:    kmsKeyEntry.CorrelationID,
			URL:              kmsKeyEntry.URL,
			ServiceToService: serviceToService,
		}
	}
	jsonBytes, err := json.Marshal(serializeMap)
	if err != nil {
		return "", err
	}
	return string(jsonBytes), nil
}

func buildIBMCloudKeyVersionKeyEntryMap(kmsKeyList []hyperv1.IBMCloudKMSKeyEntry) map[int]hyperv1.IBMCloudKMSKeyEntry {
	keyVersionKeyEntryMap := map[int]hyperv1.IBMCloudKMSKeyEntry{}
	for _, kmsKeyEntry := range kmsKeyList {
		keyVersionKeyEntryMap[kmsKeyEntry.KeyVersion] = kmsKeyEntry
	}
	return keyVersionKeyEntryMap
}

func kasContainerIBMCloudKMS() *corev1.Container {
	return &corev1.Container{
		Name: "ibmcloud-kms",
	}
}

func kasVolumeIBMCloudKMSKP() *corev1.Volume {
	return &corev1.Volume{
		Name: "ibmcloud-kms-kp",
	}
}

func kasVolumeIBMCloudKMSCustomerCredentials() *corev1.Volume {
	return &corev1.Volume{
		Name: "ibmcloud-kms-credentials",
	}
}

func kasVolumeIBMCloudKMSProjectedToken() *corev1.Volume {
	return &corev1.Volume{
		Name: podIdentityTokenIdentifier,
	}
}

func buildVolumeIBMCloudKMSCustomerCredentials(secretName string) func(*corev1.Volume) {
	return func(v *corev1.Volume) {
		v.Secret = &corev1.SecretVolumeSource{}
		v.Secret.SecretName = secretName
	}
}

func buildVolumeIBMCloudKMSKP(v *corev1.Volume) {
	v.Secret = &corev1.SecretVolumeSource{}
	v.Secret.SecretName = manifests.IBMCloudKASKMSWDEKSecret("").Name
	optionalMount := true
	v.Secret.Optional = &optionalMount
}

func buildVolumeIBMCloudKMSProjectedToken(v *corev1.Volume) {
	v.Projected = &corev1.ProjectedVolumeSource{
		Sources: []corev1.VolumeProjection{
			{
				ServiceAccountToken: &corev1.ServiceAccountTokenProjection{
					Path:              podIdentityTokenIdentifier,
					ExpirationSeconds: ptr.To[int64](900),
				},
			},
		},
	}
}

func buildKASContainerIBMCloudKMS(image string, region string, kmsInfo string, customerAPIKeyReference *corev1.EnvVarSource) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = image
		c.ImagePullPolicy = corev1.PullIfNotPresent
		c.Env = []corev1.EnvVar{
			{
				Name:  "LOG_LEVEL",
				Value: "info",
			},
			{
				Name:  "NUM_LEN_BYTES",
				Value: "4",
			},
			{
				Name:  "CACHE_TIMEOUT_IN_HOURS",
				Value: "1",
			},
			{
				Name:  "RESTART_DELAY_IN_SECONDS",
				Value: "0",
			},
			{
				Name:  "UNIX_SOCKET_PATH",
				Value: fmt.Sprintf("%s/%s", ibmCloudKMSVolumeMounts.Path(kasContainerIBMCloudKMS().Name, kasVolumeKMSSocket().Name), ibmCloudKMSUnixSocketFileName),
			},
			{
				Name:  "KP_TIMEOUT",
				Value: "10",
			},
			{
				Name:  "KP_WDEK_PATH",
				Value: fmt.Sprintf("%s/%s", ibmCloudKMSVolumeMounts.Path(kasContainerIBMCloudKMS().Name, kasVolumeIBMCloudKMSKP().Name), ibmCloudKMSWDEKSecretKeyName),
			},
			{
				Name:  "KP_STATE_PATH",
				Value: fmt.Sprintf("%s/%s", ibmCloudKMSVolumeMounts.Path(kasContainerIBMCloudKMS().Name, kasVolumeIBMCloudKMSKP().Name), ibmCloudKMSWDEKStateKeyName),
			},
			{
				Name:  "HEALTHZ_PATH",
				Value: "/healthz",
			},
			{
				Name:  "HEALTHZ_PORT",
				Value: fmt.Sprintf(":%d", ibmCloudKMSHealthPort),
			},
			{
				Name:  "KP_DATA_JSON",
				Value: kmsInfo,
			},
			{
				Name:  "REGION",
				Value: region,
			},
		}
		if customerAPIKeyReference != nil {
			c.Env = append(c.Env, corev1.EnvVar{
				Name:      "API_KEY",
				ValueFrom: customerAPIKeyReference,
			})
		}
		c.Ports = []corev1.ContainerPort{
			{
				Name:          "http",
				ContainerPort: 8001,
				Protocol:      corev1.ProtocolTCP,
			},
		}
		c.VolumeMounts = ibmCloudKMSVolumeMounts.ContainerMounts(c.Name)
		c.LivenessProbe = &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Scheme: corev1.URISchemeHTTP,
					Port:   intstr.FromInt(int(ibmCloudKMSHealthPort)),
					Path:   "healthz/liveness",
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

func (p *ibmCloudKMSProvider) GenerateKMSPodConfig() (*KMSPodConfig, error) {
	kmsKPInfo, err := buildIBMCloudKMSInfoEnvVar(buildIBMCloudKeyVersionKeyEntryMap(p.ibmCloud.KeyList), p.ibmCloud.Auth.Type)
	if err != nil {
		return nil, fmt.Errorf("failed to generate kmsKPInfo env var: %w", err)
	}

	podConfig := &KMSPodConfig{}

	podConfig.Volumes = append(podConfig.Volumes, util.BuildVolume(kasVolumeKMSSocket(), buildVolumeKMSSocket), util.BuildVolume(kasVolumeIBMCloudKMSKP(), buildVolumeIBMCloudKMSKP), util.BuildVolume(kasVolumeIBMCloudKMSProjectedToken(), buildVolumeIBMCloudKMSProjectedToken))
	var customerAPIKeyReference *corev1.EnvVarSource
	switch p.ibmCloud.Auth.Type {
	case hyperv1.IBMCloudKMSUnmanagedAuth:
		if len(p.ibmCloud.Auth.Unmanaged.Credentials.Name) == 0 {
			return nil, fmt.Errorf("ibmcloud kms credential not specified")
		}
		customerAPIKeyReference = &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: p.ibmCloud.Auth.Unmanaged.Credentials.Name,
				},
				Key: hyperv1.IBMCloudIAMAPIKeySecretKey,
			},
		}
		podConfig.Volumes = append(podConfig.Volumes, util.BuildVolume(kasVolumeIBMCloudKMSCustomerCredentials(), buildVolumeIBMCloudKMSCustomerCredentials(p.ibmCloud.Auth.Unmanaged.Credentials.Name)))
	case hyperv1.IBMCloudKMSManagedAuth:
	default:
		return nil, fmt.Errorf("unrecognized ibmcloud kms auth type %s", p.ibmCloud.Auth.Type)
	}
	podConfig.Containers = append(podConfig.Containers, util.BuildContainer(kasContainerIBMCloudKMS(), buildKASContainerIBMCloudKMS(p.kmsImage, p.ibmCloud.Region, kmsKPInfo, customerAPIKeyReference)))

	podConfig.KASContainerMutate = func(c *corev1.Container) {
		c.VolumeMounts = append(c.VolumeMounts, ibmCloudKMSVolumeMounts.ContainerMounts(KasMainContainerName)...)
		c.Args = append(c.Args, "--encryption-provider-config-automatic-reload=false")
	}

	return podConfig, nil
}
