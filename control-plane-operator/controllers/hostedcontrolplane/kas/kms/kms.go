package kms

import (
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/apiserver/pkg/apis/apiserver/v1"
)

type IKMSProvider interface {
	GenerateKMSEncryptionConfig(apiVersion string) (*v1.EncryptionConfiguration, error)

	ApplyKMSConfig(podSpec *corev1.PodSpec) error
}

const (
	KasMainContainerName        = "kube-apiserver"
	encryptionConfigurationKind = "EncryptionConfiguration"

	kasVolumeLocalhostKubeconfig = "localhost-kubeconfig"
)

func kasVolumeKMSSocket() *corev1.Volume {
	return &corev1.Volume{
		Name: "kms-socket",
	}
}

func buildVolumeKMSSocket(v *corev1.Volume) {
	v.EmptyDir = &corev1.EmptyDirVolumeSource{}
}
