package manifests

import (
	"fmt"

	"k8s.io/apimachinery/pkg/types"
)

func HostedControlPlaneNamespaceName(hostedClusterNamespace, hostedClusterName string) types.NamespacedName {
	return types.NamespacedName{
		Name: fmt.Sprintf("%s-%s", hostedClusterNamespace, hostedClusterName),
	}
}

func KubeConfigSecretName(hostedClusterNamespace string, hostedClusterName string) types.NamespacedName {
	return types.NamespacedName{
		Namespace: hostedClusterNamespace,
		Name:      hostedClusterName + "-admin-kubeconfig",
	}
}

func DefaultNodePoolName(hostedClusterNamespace, hostedClusterName string) types.NamespacedName {
	return types.NamespacedName{
		Namespace: hostedClusterNamespace,
		Name:      hostedClusterName,
	}
}
