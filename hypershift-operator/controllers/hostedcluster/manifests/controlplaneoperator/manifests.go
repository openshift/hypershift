package controlplaneoperator

import (
	"k8s.io/apimachinery/pkg/types"
)

func OperatorDeploymentName(controlPlaneOperatorNamespace string) types.NamespacedName {
	return types.NamespacedName{
		Namespace: controlPlaneOperatorNamespace,
		Name:      "control-plane-operator",
	}
}

func OperatorServiceAccountName(controlPlaneOperatorNamespace string) types.NamespacedName {
	return types.NamespacedName{
		Namespace: controlPlaneOperatorNamespace,
		Name:      "control-plane-operator",
	}
}

func OperatorClusterRoleName() types.NamespacedName {
	return types.NamespacedName{
		Name: "control-plane-operator",
	}
}

func OperatorClusterRoleBindingName(controlPlaneOperatorNamespace string) types.NamespacedName {
	return types.NamespacedName{
		Name: "control-plane-operator-" + controlPlaneOperatorNamespace,
	}
}

func OperatorRoleName(controlPlaneOperatorNamespace string) types.NamespacedName {
	return types.NamespacedName{
		Namespace: controlPlaneOperatorNamespace,
		Name:      "control-plane-operator",
	}
}

func OperatorRoleBindingName(controlPlaneOperatorNamespace string) types.NamespacedName {
	return types.NamespacedName{
		Namespace: controlPlaneOperatorNamespace,
		Name:      "control-plane-operator",
	}
}

func CAPIClusterName(controlPlaneOperatorNamespace string, infraID string) types.NamespacedName {
	return types.NamespacedName{
		Namespace: controlPlaneOperatorNamespace,
		Name:      infraID,
	}
}

func HostedControlPlaneName(controlPlaneNamespace string, hostedClusterName string) types.NamespacedName {
	return types.NamespacedName{
		Namespace: controlPlaneNamespace,
		Name:      hostedClusterName,
	}
}

func ExternalInfraClusterName(controlPlaneNamespace string, hostedClusterName string) types.NamespacedName {
	return types.NamespacedName{
		Namespace: controlPlaneNamespace,
		Name:      hostedClusterName,
	}
}

func ProviderCredentialsName(controlPlaneNamespace string) types.NamespacedName {
	return types.NamespacedName{
		Namespace: controlPlaneNamespace,
		Name:      "provider-creds",
	}
}

func PullSecretName(controlPlaneNamespace string) types.NamespacedName {
	return types.NamespacedName{
		Namespace: controlPlaneNamespace,
		Name:      "pull-secret",
	}
}

func SigningKeyName(controlPlaneNamespace string) types.NamespacedName {
	return types.NamespacedName{
		Namespace: controlPlaneNamespace,
		Name:      "signing-key",
	}
}

func SSHKeyName(controlPlaneNamespace string) types.NamespacedName {
	return types.NamespacedName{
		Namespace: controlPlaneNamespace,
		Name:      "ssh-key",
	}
}
