package nodepool

import (
	"fmt"

	npconstants "github.com/openshift/hypershift/pkg/nodepool"
	"github.com/openshift/hypershift/support/netutil"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	EC2VolumeDefaultSize int64  = 16
	EC2VolumeDefaultType string = "gp3"
)

func TunedConfigMap(namespace, name string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      fmt.Sprintf("tuned-%s", name),
		},
	}
}

func PerformanceProfileConfigMap(namespace, name, nodePoolName string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      netutil.ShortenName(name, nodePoolName, npconstants.QualifiedNameMaxLength),
		},
	}
}

const tokenSecretPrefix = "token"

func TokenSecret(namespace, name, payloadInputHash string) *corev1.Secret {
	return namedSecret(namespace, fmt.Sprintf("%s-%s-%s", tokenSecretPrefix, name, payloadInputHash))
}

func namedSecret(namespace, name string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      name,
		},
	}
}
