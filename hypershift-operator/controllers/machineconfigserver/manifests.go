package machineconfigserver

import (
	"fmt"

	routev1 "github.com/openshift/api/route/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func MachineConfigServerDeployment(machineConfigServerNamespace, machineConfigServerName string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: machineConfigServerNamespace,
			Name:      fmt.Sprintf("machine-config-server-%s", machineConfigServerName),
		},
	}
}

func MachineConfigServerServiceAccount(machineConfigServerNamespace, machineConfigServerName string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: machineConfigServerNamespace,
			Name:      fmt.Sprintf("machine-config-server-%s", machineConfigServerName),
		},
	}
}

func MachineConfigServerRoleBinding(machineConfigServerNamespace, machineConfigServerName string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: machineConfigServerNamespace,
			Name:      fmt.Sprintf("machine-config-server-%s", machineConfigServerName),
		},
	}
}

func MachineConfigServerService(machineConfigServerNamespace, machineConfigServerName string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: machineConfigServerNamespace,
			Name:      fmt.Sprintf("machine-config-server-%s", machineConfigServerName),
		},
	}
}

func MachineConfigServerIgnitionRoute(machineConfigServerNamespace, machineConfigServerName string) *routev1.Route {
	return &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: machineConfigServerNamespace,
			Name:      fmt.Sprintf("ignition-provider-%s", machineConfigServerName),
		},
	}
}

func MachineConfigServerUserDataSecret(machineConfigServerNamespace, machineConfigServerName string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: machineConfigServerNamespace,
			Name:      fmt.Sprintf("user-data-%s", machineConfigServerName),
		},
	}
}
