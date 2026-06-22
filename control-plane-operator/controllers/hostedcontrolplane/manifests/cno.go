package manifests

import (
	routev1 "github.com/openshift/api/route/v1"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const clusterNetworkOperator = "cluster-network-operator"
const multusAdmissionController = "multus-admission-controller"
const networkNodeIdentity = "network-node-identity"
const ovnKubeControlPlane = "ovnkube-control-plane"

func ClusterNetworkOperatorDeployment(ns string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      clusterNetworkOperator,
			Namespace: ns,
		},
	}
}

func ClusterNetworkOperatorRole(namespace string) *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      clusterNetworkOperator,
		},
	}
}

func ClusterNetworkOperatorRoleBinding(namespace string) *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      clusterNetworkOperator,
		},
	}
}

func ClusterNetworkOperatorServiceAccount(namespace string) *corev1.ServiceAccount {
	return &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      clusterNetworkOperator,
		},
	}
}

func MultusAdmissionControllerDeployment(namespace string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      multusAdmissionController,
		},
	}
}

func NetworkNodeIdentityDeployment(namespace string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      networkNodeIdentity,
		},
	}
}

func OVNKubeControlPlaneDeployment(namespace string) *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      ovnKubeControlPlane,
		},
	}
}

func OVNKubeSBDBRoute(namespace string) *routev1.Route {
	return &routev1.Route{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      "ovnkube-sbdb",
		},
	}
}

func MasterExternalService(namespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      "ovnkube-master-external",
		},
	}
}

func MasterInternalService(namespace string) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: namespace,
			Name:      "ovnkube-master-internal",
		},
	}
}
