package globalconfig

import (
	configv1 "github.com/openshift/api/config/v1"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func BuildConfig() *configv1.Build {
	return &configv1.Build{
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
	}
}

func ObservedBuildConfig(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "observed-config-build",
			Namespace: ns,
		},
	}
}
