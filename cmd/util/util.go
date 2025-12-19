package util

import (
	"fmt"
	"strconv"
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

// ParseTolerationString parses a toleration string and returns a corev1.Toleration object
func ParseTolerationString(s string) *corev1.Toleration {
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return nil
	}

	toleration := corev1.Toleration{}
	setTolerationKeyValueAndEffect(parts, &toleration)

	// If toleration seconds is provided
	if len(parts) == 3 {
		if tolerationSeconds, err := strconv.ParseInt(parts[2], 10, 64); err == nil && tolerationSeconds > 0 {
			toleration.TolerationSeconds = &tolerationSeconds
		}
	}

	return &toleration
}

func setTolerationKeyValueAndEffect(tolerationStrParts []string, tolerationObj *corev1.Toleration) {
	if strings.Contains(tolerationStrParts[0], "=") {
		kv := strings.SplitN(tolerationStrParts[0], "=", 2)
		tolerationObj.Key = kv[0]
		tolerationObj.Value = kv[1]
		tolerationObj.Operator = corev1.TolerationOpEqual
	} else {
		tolerationObj.Key = tolerationStrParts[0]
		tolerationObj.Operator = corev1.TolerationOpExists
	}

	tolerationObj.Effect = corev1.TaintEffect(tolerationStrParts[1])
}
