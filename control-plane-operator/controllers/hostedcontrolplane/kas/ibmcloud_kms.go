package kas

import (
	"bytes"
	"encoding/json"
	"fmt"
	hyperv1 "github.com/openshift/hypershift/api/v1alpha1"
	"github.com/openshift/hypershift/control-plane-operator/api"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1 "k8s.io/apiserver/pkg/apis/config/v1"
	"k8s.io/utils/pointer"
	"sort"
	"strconv"
	"time"
)

var (
	ibmCloudKMSVolumeMounts = util.PodVolumeMounts{
		kasContainerMain().Name: {
			kasVolumeKMSSocket().Name: "/tmp",
		},
		kasContainerIBMCloudKMS().Name: {
			kasVolumeKMSSocket().Name:     "/tmp",
			kasVolumeIBMCloudKMSKP().Name: "/tmp/kp",
		},
	}
)

const (
	ibmCloudKMSUnixSocket = "unix:///tmp/keyprotectprovider.sock"
	ibmKeyNamePrefix      = "ibm"
	ibmCloudKMSHealthPort = 8081
)

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

func generateIBMCloudKMSEncryptionConfig(kmsKeyList []hyperv1.IBMCloudKMSKeyEntry) ([]byte, error) {
	if len(kmsKeyList) == 0 {
		return nil, fmt.Errorf("no keys specified")
	}
	keyVersionKeyEntryMap := buildIBMCloudKeyVersionKeyEntryMap(kmsKeyList)
	keys := make([]int, 0, len(keyVersionKeyEntryMap))
	for k := range keyVersionKeyEntryMap {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	var providerConfiguration []v1.ProviderConfiguration
	// iterate in reverse because highest version key should be used for new secret encryption
	for i := len(keys) - 1; i >= 0; i-- {
		configEntry := v1.ProviderConfiguration{
			KMS: &v1.KMSConfiguration{
				Name:      fmt.Sprintf("%s%d", ibmKeyNamePrefix, keyVersionKeyEntryMap[keys[i]].KeyVersion),
				Endpoint:  ibmCloudKMSUnixSocket,
				CacheSize: pointer.Int32Ptr(100),
				Timeout:   &metav1.Duration{Duration: 35 * time.Second},
			},
		}
		providerConfiguration = append(providerConfiguration, configEntry)
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
	err := api.YamlSerializer.Encode(&encryptionConfig, bufferInstance)
	if err != nil {
		return nil, err
	}
	return bufferInstance.Bytes(), nil
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

func buildKASContainerIBMCloudKMS(image string, region string, kmsInfo string, customerAPIKeyReference *corev1.EnvVarSource) func(c *corev1.Container) {
	return func(c *corev1.Container) {
		c.Image = image
		c.ImagePullPolicy = corev1.PullAlways
		c.Env = []corev1.EnvVar{
			corev1.EnvVar{
				Name:  "LOG_LEVEL",
				Value: "info",
			},
			corev1.EnvVar{
				Name:  "NUM_LEN_BYTES",
				Value: "4",
			},
			corev1.EnvVar{
				Name:  "CACHE_TIMEOUT_IN_HOURS",
				Value: "1",
			},
			corev1.EnvVar{
				Name:  "RESTART_DELAY_IN_SECONDS",
				Value: "0",
			},
			corev1.EnvVar{
				Name:  "UNIX_SOCKET_PATH",
				Value: "/tmp/keyprotectprovider.sock",
			},
			corev1.EnvVar{
				Name:  "KP_TIMEOUT",
				Value: "10",
			},
			corev1.EnvVar{
				Name:  "KP_WDEK_PATH",
				Value: "/tmp/kp/wdek",
			},
			corev1.EnvVar{
				Name:  "KP_STATE_PATH",
				Value: "/tmp/kp/state",
			},
			corev1.EnvVar{
				Name:  "HEALTHZ_PATH",
				Value: "/healthz",
			},
			corev1.EnvVar{
				Name:  "HEALTHZ_PORT",
				Value: fmt.Sprintf(":%d", ibmCloudKMSHealthPort),
			},
			corev1.EnvVar{
				Name:  "KP_DATA_JSON",
				Value: kmsInfo,
			},
			corev1.EnvVar{
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
	}
}

func applyIBMCloudKMSConfig(podSpec *corev1.PodSpec, ibmCloud *hyperv1.IBMCloudKMSSpec, kmsImage string) error {
	if ibmCloud == nil || len(ibmCloud.KeyList) == 0 || ibmCloud.Auth == nil || len(ibmCloud.Region) == 0 || len(kmsImage) == 0 {
		return fmt.Errorf("ibmcloud kms metadata not specified")
	}
	kmsKPInfo, err := buildIBMCloudKMSInfoEnvVar(buildIBMCloudKeyVersionKeyEntryMap(ibmCloud.KeyList), ibmCloud.Auth.Type)
	if err != nil {
		return fmt.Errorf("failed to generate kmsKPInfo env var: %w", err)
	}
	podSpec.Volumes = append(podSpec.Volumes, util.BuildVolume(kasVolumeKMSSocket(), buildVolumeKMSSocket))
	var customerAPIKeyReference *corev1.EnvVarSource
	switch ibmCloud.Auth.Type {
	case hyperv1.IBMCloudKMSUnmanagedAuth:
		if len(ibmCloud.Auth.Unmanaged.Credentials.Name) == 0 {
			return fmt.Errorf("ibmcloud kms credential not specified")
		}
		customerAPIKeyReference = &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: ibmCloud.Auth.Unmanaged.Credentials.Name,
				},
				Key: hyperv1.IBMCloudIAMAPIKeySecretKey,
			},
		}
		podSpec.Volumes = append(podSpec.Volumes, util.BuildVolume(kasVolumeIBMCloudKMSCustomerCredentials(), buildVolumeIBMCloudKMSCustomerCredentials(ibmCloud.Auth.Unmanaged.Credentials.Name)))
	case hyperv1.IBMCloudKMSManagedAuth:
		podSpec.Volumes = append(podSpec.Volumes, util.BuildVolume(kasVolumeIBMCloudKMSKP(), buildVolumeIBMCloudKMSKP))
	default:
		return fmt.Errorf("unrecognized ibmcloud kms auth type %s", ibmCloud.Auth.Type)
	}
	podSpec.Containers = append(podSpec.Containers, util.BuildContainer(kasContainerIBMCloudKMS(), buildKASContainerIBMCloudKMS(kmsImage, ibmCloud.Region, kmsKPInfo, customerAPIKeyReference)))
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
		ibmCloudKMSVolumeMounts.ContainerMounts(kasContainerMain().Name)...)
	return nil
}
