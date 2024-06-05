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

func PerformanceProfileConfigMap(namespace, name, nodePoolName string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      ComposeValidName(name, nodePoolName),
		},
	}
}

// 63 is the qualifiedNameMaxLength allowed for k8s object
// https://github.com/kubernetes/kubernetes/blob/957c9538670b5f7ead2c9ba9ceb9de081d66caa4/staging/src/k8s.io/apimachinery/pkg/util/validation/validation.go#L34
const qualifiedNameMaxLength = 63

// ComposeValidName takes two qualified objects` names
func ComposeValidName(name1, name2 string) string {
	totalLen := len(name1) + len(name2)
	// it cannot be equal to qualifiedNameMaxLength because we should preserve 1 character for the dash '-'
	if totalLen >= qualifiedNameMaxLength {
		weight1 := float64(len(name1) / totalLen)
		weight2 := float64(len(name2) / totalLen)

		prefixLen1 := int(weight1 * float64(qualifiedNameMaxLength-1))
		prefixLen2 := int(weight2 * float64(qualifiedNameMaxLength-1))

		// handle rounding cases
		if prefixLen1 == 0 {
			prefixLen1++
			// preserve a character for the dash
			prefixLen2 = qualifiedNameMaxLength - 1 - prefixLen1
		}
		if prefixLen2 == 0 {
			prefixLen2++
			// preserve a character for the dash
			prefixLen1 = qualifiedNameMaxLength - 1 - prefixLen1
		}
		// ensure prefix lengths do not exceed the actual lengths of the names
		if prefixLen1 > len(name1) {
			prefixLen1 = len(name1)
		}
		if prefixLen2 > len(name2) {
			prefixLen2 = len(name2)
		}
		return fmt.Sprintf("%s-%s", name1[:prefixLen1], name2[:prefixLen2])
	}
	return fmt.Sprintf("%s-%s", name1, name2)
}
