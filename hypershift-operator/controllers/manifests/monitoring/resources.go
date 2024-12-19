package monitoring

import (
	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func MonitoringNamespace() *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "openshift-monitoring",
		},
	}
}

func UWMNamespace() *corev1.Namespace {
	return &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "openshift-user-workload-monitoring",
		},
	}
}

func MonitoringConfig() *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "cluster-monitoring-config",
			Namespace: MonitoringNamespace().Name,
		},
	}
}

func UWMConfig() *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-workload-monitoring-config",
			Namespace: UWMNamespace().Name,
		},
	}
}

func TelemeterClientSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "telemeter-client",
			Namespace: MonitoringNamespace().Name,
		},
	}
}

func TelemetryConfigRules() *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "telemetry-config",
			Namespace: MonitoringNamespace().Name,
		},
	}
}

func UWMRemoteWriteSecret() *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "telemetry-remote-write",
			Namespace: UWMNamespace().Name,
		},
	}
}

func ClusterVersion() *configv1.ClusterVersion {
	return &configv1.ClusterVersion{
		ObjectMeta: metav1.ObjectMeta{
			Name: "version",
		},
	}
}
