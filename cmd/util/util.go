package util

import (
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ValidateRequiredOption returns a cobra style error message when the flag value is empty
func ValidateRequiredOption(flag string, value string) error {
	if len(value) == 0 {
		return fmt.Errorf("required flag(s) \"%s\" not set", flag)
	}
	return nil
}

func SecretResource(namespace, name string) *corev1.Secret {
	return &corev1.Secret{
		TypeMeta: metav1.TypeMeta{
			APIVersion: corev1.SchemeGroupVersion.String(),
			Kind:       "Secret",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
}

func ConfigMapResource(namespace, name string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		TypeMeta: metav1.TypeMeta{
			APIVersion: corev1.SchemeGroupVersion.String(),
			Kind:       "ConfigMap",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
}

// ParseTolerationString parses a toleration string in the format "key=value:effect" or "key:effect"
// and returns a corev1.Toleration object. Returns nil if the format is invalid.
func ParseTolerationString(s string) *corev1.Toleration {
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return nil
	}

	toleration := corev1.Toleration{
		Effect: corev1.TaintEffect(parts[1]),
	}

	if strings.Contains(parts[0], "=") {
		kv := strings.SplitN(parts[0], "=", 2)
		toleration.Key = kv[0]
		toleration.Value = kv[1]
		toleration.Operator = corev1.TolerationOpEqual
	} else {
		toleration.Key = parts[0]
		toleration.Operator = corev1.TolerationOpExists
	}

	return &toleration
}
