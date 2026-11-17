package cpo

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	KubeAPIServerServiceName = "kube-apiserver"

	// KubeAPIServerConfigMapName is the ConfigMap in the control plane namespace holding the
	// rendered kube-apiserver configuration.
	KubeAPIServerConfigMapName = "kas-config"

	// KubeAPIServerConfigKey is the key within KubeAPIServerConfigMapName holding the serialized
	// KubeAPIServerConfig.
	KubeAPIServerConfigKey = "config.json"
)

func KubeAPIServerConfigMap(controlPlaneNamespace string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      KubeAPIServerConfigMapName,
			Namespace: controlPlaneNamespace,
		},
	}
}

func KASCustomKubeconfigSecret(controlPlaneNamespace string, ref *hyperv1.KubeconfigSecretRef) *corev1.Secret {
	s := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "custom-admin-kubeconfig",
			Namespace: controlPlaneNamespace,
		},
	}
	if ref != nil {
		s.Name = ref.Name
	}
	return s
}

func KubeAPIServerService(hostedClusterNamespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      KubeAPIServerServiceName,
			Namespace: hostedClusterNamespace,
		},
	}
}

func KubeAPIServerServiceAzureLB(hostedClusterNamespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      KubeAPIServerServiceName + "lb",
			Namespace: hostedClusterNamespace,
		},
	}
}
