package util

import (
	"fmt"
	"net"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ValidateIPv4CIDRs validates that each CIDR in the slice is a valid IPv4 CIDR.
// It returns an error listing all invalid entries.
func ValidateIPv4CIDRs(cidrs []string) error {
	var msgs []string
	for _, cidr := range cidrs {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			msgs = append(msgs, fmt.Sprintf("%q: %v", cidr, err))
			continue
		}
		if ipNet.IP.To4() == nil {
			msgs = append(msgs, fmt.Sprintf("%q: IPv6 CIDRs are not supported", cidr))
		}
	}
	if len(msgs) > 0 {
		return fmt.Errorf("%s", strings.Join(msgs, "; "))
	}
	return nil
}

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
