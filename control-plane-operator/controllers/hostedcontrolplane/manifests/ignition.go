package manifests

import (
	routev1 "github.com/openshift/api/route/v1"
	prometheusoperatorv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	mcfgv1 "github.com/openshift/hypershift/thirdparty/machineconfigoperator/pkg/apis/machineconfiguration.openshift.io/v1"
)

const (
	IgnitionServerResourceName = "ignition-server"

	// TokenSecretKey is the data key for the ignition token secret.
	TokenSecretKey = "token"
)

func MachineConfigFIPS() *mcfgv1.MachineConfig {
	return &mcfgv1.MachineConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: "30-fips",
		},
	}
}

func MachineConfigWorkerSSH() *mcfgv1.MachineConfig {
	return &mcfgv1.MachineConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: "99-worker-ssh",
		},
	}
}

func IgnitionWorkerSSHConfig(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ignition-config-worker-ssh",
			Namespace: ns,
		},
	}
}

func IgnitionFIPSConfig(ns string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ignition-config-fips",
			Namespace: ns,
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

func IgnitionServerRoute(namespace string) *routev1.Route {
	return &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      IgnitionServerResourceName,
		},
	}
}

func IgnitionServerService(namespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      IgnitionServerResourceName,
		},
	}
}

func IgnitionServerPodMonitor(controlPlaneNamespace string) *prometheusoperatorv1.PodMonitor {
	return &prometheusoperatorv1.PodMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      "ignition-server",
		},
	}
}

func IgnitionServerDeployment(namespace string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      IgnitionServerResourceName,
		},
	}
}

func IgnitionServerIgnitionCACertSecret(namespace string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      IgnitionServerResourceName + "-ca-cert",
		},
	}
}

func IgnitionServingCertSecret(namespace string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      "ignition-server-serving-cert",
		},
	}
}

func IgnitionServerServiceAccount(namespace string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      IgnitionServerResourceName,
		},
	}
}

func IgnitionServerRole(namespace string) *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      IgnitionServerResourceName,
		},
	}
}

func IgnitionServerRoleBinding(namespace string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      IgnitionServerResourceName,
		},
	}
}
