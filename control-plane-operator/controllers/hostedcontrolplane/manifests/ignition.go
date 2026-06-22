package manifests

import (
	mcfgv1 "github.com/openshift/api/machineconfiguration/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

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

func ImageContentPolicyIgnitionConfig(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ignition-config-40-image-content-source",
			Namespace: ns,
		},
	}
}
