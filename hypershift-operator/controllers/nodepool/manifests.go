package nodepool

import (
	"fmt"

	"github.com/openshift/hypershift/support/util"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	EC2VolumeDefaultSize int64  = 16
	EC2VolumeDefaultType string = "gp3"

	// QualifiedNameMaxLength is the maximal name length allowed for k8s object
	// https://github.com/kubernetes/kubernetes/blob/957c9538670b5f7ead2c9ba9ceb9de081d66caa4/staging/src/k8s.io/apimachinery/pkg/util/validation/validation.go#L34
	QualifiedNameMaxLength = 63
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
			Name:      util.ShortenName(name, nodePoolName, QualifiedNameMaxLength),
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
