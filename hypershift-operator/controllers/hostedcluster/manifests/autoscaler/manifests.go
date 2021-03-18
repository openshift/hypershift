package autoscaler

import (
	"k8s.io/apimachinery/pkg/types"
)

func AutoScalerDeploymentName(controlPlaneNamespace string) types.NamespacedName {
	return types.NamespacedName{
		Namespace: controlPlaneNamespace,
		Name:      "cluster-autoscaler",
	}
}

func AutoScalerServiceAccountName(controlPlaneNamespace string) types.NamespacedName {
	return types.NamespacedName{
		Namespace: controlPlaneNamespace,
		Name:      "cluster-autoscaler",
	}
}

func AutoScalerRoleName(controlPlaneNamespace string) types.NamespacedName {
	return types.NamespacedName{
		Namespace: controlPlaneNamespace,
		Name:      "cluster-autoscaler-management",
	}
}

func AutoScalerRoleBindingName(controlPlaneNamespace string) types.NamespacedName {
	return types.NamespacedName{
		Namespace: controlPlaneNamespace,
		Name:      "cluster-autoscaler-management",
	}
}
