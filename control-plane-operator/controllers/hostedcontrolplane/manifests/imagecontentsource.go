package manifests

import (
	"github.com/openshift/api/operator/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	IgnitionConfigKey           = "config"
	CoreIgnitionFieldLabelKey   = "hypershift.openshift.io/core-ignition-config"
	CoreIgnitionFieldLabelValue = "true"
)

func ImageContentSourcePolicy() *v1alpha1.ImageContentSourcePolicy {
	return &v1alpha1.ImageContentSourcePolicy{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ImageContentSourcePolicy",
			APIVersion: v1alpha1.GroupVersion.String(),
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: "cluster",
		},
	}
}

func ImageContentSourcePolicyIgnitionConfig(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ignition-config-40-image-content-source",
			Namespace: ns,
		},
	}
}

func ImageContentSourcePolicyUserManifest(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "user-manifest-image-content-source-policy",
			Namespace: ns,
		},
	}
}
