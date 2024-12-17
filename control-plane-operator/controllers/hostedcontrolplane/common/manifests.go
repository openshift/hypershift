package common

import (
	"github.com/openshift/hypershift/control-plane-operator/controllers/hostedcontrolplane/manifests"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func PullSecret(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pull-secret",
			Namespace: ns,
		},
	}
}

func DefaultServiceAccount(ns string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "default",
			Namespace: ns,
		},
	}
}

func KubeadminPasswordSecret(ns string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "kubeadmin-password",
			Namespace: ns,
		},
	}
}

func VolumeTotalClientCA() *corev1.Volume {
	return &corev1.Volume{
		Name: "client-ca",
	}
}

func BuildVolumeTotalClientCA(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	v.ConfigMap.Name = manifests.TotalClientCABundle("").Name
}

func VolumeAggregatorCA() *corev1.Volume {
	return &corev1.Volume{
		Name: "aggregator-ca",
	}
}
func BuildVolumeAggregatorCA(v *corev1.Volume) {
	v.ConfigMap = &corev1.ConfigMapVolumeSource{}
	v.ConfigMap.Name = manifests.AggregatorClientCAConfigMap("").Name
}
