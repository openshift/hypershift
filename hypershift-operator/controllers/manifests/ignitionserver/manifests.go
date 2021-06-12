package ignitionserver

import (
	routev1 "github.com/openshift/api/route/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	ResourceName = "ignition-server"

	// TokenSecretKey is the data key for the ignition token secret.
	TokenSecretKey = "token"
)

func Route(namespace string) *routev1.Route {
	return &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      ResourceName,
		},
	}
}

func Service(namespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      ResourceName,
		},
	}
}

func Deployment(namespace string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      ResourceName,
		},
	}
}

func IgnitionCACertSecret(namespace string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      ResourceName + "-ca-cert",
		},
	}
}

func IgnitionServingCertSecret(namespace string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      ResourceName + "-serving-cert",
		},
	}
}

// IgnitionTokenSecret returns metadata for the ignition token secret. The key
// of the secret must be "token".
func IgnitionTokenSecret(namespace string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      ResourceName + "-token",
		},
	}
}
