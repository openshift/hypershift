package clusterapi

import (
	"k8s.io/apimachinery/pkg/types"
)

func ClusterAPIManagerDeploymentName(controlPlaneNamespace string) types.NamespacedName {
	return types.NamespacedName{
		Namespace: controlPlaneNamespace,
		Name:      "cluster-api",
	}
}

func CAPIManagerServiceAccountName(controlPlaneNamespace string) types.NamespacedName {
	return types.NamespacedName{
		Namespace: controlPlaneNamespace,
		Name:      "cluster-api",
	}
}

func CAPIManagerClusterRoleName(controlPlaneNamespace string) types.NamespacedName {
	return types.NamespacedName{
		Name: "cluster-api",
	}
}

func CAPIManagerClusterRoleBindingName(controlPlaneNamespace string) types.NamespacedName {
	return types.NamespacedName{
		Name: "cluster-api-" + controlPlaneNamespace,
	}
}

func CAPIManagerRoleName(controlPlaneNamespace string) types.NamespacedName {
	return types.NamespacedName{
		Namespace: controlPlaneNamespace,
		Name:      "cluster-api",
	}
}

func CAPIManagerRoleBindingName(controlPlaneNamespace string) types.NamespacedName {
	return types.NamespacedName{
		Namespace: controlPlaneNamespace,
		Name:      "cluster-api",
	}
}

func CAPIAWSProviderDeploymentName(controlPlaneNamespace string) types.NamespacedName {
	return types.NamespacedName{
		Namespace: controlPlaneNamespace,
		Name:      "capa-controller-manager",
	}
}

func CAPIAWSProviderServiceAccountName(controlPlaneNamespace string) types.NamespacedName {
	return types.NamespacedName{
		Namespace: controlPlaneNamespace,
		Name:      "capa-controller-manager",
	}
}

func CAPIAWSProviderRoleName(controlPlaneNamespace string) types.NamespacedName {
	return types.NamespacedName{
		Namespace: controlPlaneNamespace,
		Name:      "capa-manager",
	}
}

func CAPIAWSProviderRoleBindingName(controlPlaneNamespace string) types.NamespacedName {
	return types.NamespacedName{
		Namespace: controlPlaneNamespace,
		Name:      "capa-manager",
	}
}
