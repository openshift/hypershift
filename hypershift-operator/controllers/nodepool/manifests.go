package nodepool

import (
	"fmt"

	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	capiv1 "sigs.k8s.io/cluster-api/api/v1beta1"
)

const (
	EC2VolumeDefaultSize int64  = 16
	EC2VolumeDefaultType string = "gp3"
)

func machineDeployment(nodePool *hyperv1.NodePool, controlPlaneNamespace string) *capiv1.MachineDeployment {
	return &capiv1.MachineDeployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      nodePool.GetName(),
			Namespace: controlPlaneNamespace,
		},
	}
}

func machineSet(nodePool *hyperv1.NodePool, controlPlaneNamespace string) *capiv1.MachineSet {
	return &capiv1.MachineSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      nodePool.GetName(),
			Namespace: controlPlaneNamespace,
		},
	}
}

func machineHealthCheck(nodePool *hyperv1.NodePool, controlPlaneNamespace string) *capiv1.MachineHealthCheck {
	return &capiv1.MachineHealthCheck{
		TypeMeta: metav1.TypeMeta{},
		ObjectMeta: metav1.ObjectMeta{
			Name:      nodePool.GetName(),
			Namespace: controlPlaneNamespace,
		},
	}
}

const ignitionUserDataPrefix = "user-data"

func IgnitionUserDataSecret(namespace, name, payloadInputHash string) *corev1.Secret {
	return namedSecret(namespace, fmt.Sprintf("%s-%s-%s", ignitionUserDataPrefix, name, payloadInputHash))
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

func TunedConfigMap(namespace, name string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      fmt.Sprintf("tuned-%s", name),
		},
	}
}

func PerformanceProfileConfigMap(namespace, name string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      "perfprofile-" + name,
		},
	}
}
