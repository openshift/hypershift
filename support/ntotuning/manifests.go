package ntotuning

import (
	"fmt"

	"github.com/openshift/hypershift/support/netutil"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const qualifiedNameMaxLength = 63

// TunedConfigMap returns the NTO input ConfigMap name for a node pool in the hosted control plane namespace.
func TunedConfigMap(namespace, name string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      fmt.Sprintf("tuned-%s", name),
		},
	}
}

// PerformanceProfileConfigMap returns the NTO input ConfigMap for a PerformanceProfile in the hosted control plane namespace.
func PerformanceProfileConfigMap(namespace, name, nodePoolName string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      netutil.ShortenName(name, nodePoolName, qualifiedNameMaxLength),
		},
	}
}
