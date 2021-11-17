package manifests

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mcfgv1 "github.com/openshift/hypershift/thirdparty/machineconfigoperator/pkg/apis/machineconfiguration.openshift.io/v1"
)

func MachineConfigAPIServerHAProxy() *mcfgv1.MachineConfig {
	return &mcfgv1.MachineConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: "20-apiserver-haproxy",
		},
	}
}

func MachineConfigFIPS() *mcfgv1.MachineConfig {
	return &mcfgv1.MachineConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: "30-fips",
		},
	}
}

func MachineConfigWorkerSSH() *mcfgv1.MachineConfig {
	return &mcfgv1.MachineConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: "99-worker-ssh",
		},
	}
}

func IgnitionAPIServerHAProxyConfig(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ignition-config-apiserver-haproxy",
			Namespace: ns,
		},
	}
}

func IgnitionWorkerSSHConfig(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ignition-config-worker-ssh",
			Namespace: ns,
		},
	}
}

func IgnitionFIPSConfig(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ignition-config-fips",
			Namespace: ns,
		},
	}
}
