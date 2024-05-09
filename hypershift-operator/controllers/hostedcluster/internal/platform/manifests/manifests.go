package manifests

import (
	"k8s.io/api/core/v1"
	v12 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func KubeCloudControllerCredsSecret(controlPlaneNamespace string) *v1.Secret {
	return &v1.Secret{
		ObjectMeta: v12.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      "cloud-controller-creds",
		},
	}
}

func NodePoolManagementCredsSecret(controlPlaneNamespace string) *v1.Secret {
	return &v1.Secret{
		ObjectMeta: v12.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      "node-management-creds",
		},
	}
}

func ControlPlaneOperatorCredsSecret(controlPlaneNamespace string) *v1.Secret {
	return &v1.Secret{
		ObjectMeta: v12.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      "control-plane-operator-creds",
		},
	}
}

func CloudNetworkConfigControllerCredsSecret(controlPlaneNamespace string) *v1.Secret {
	return &v1.Secret{
		ObjectMeta: v12.ObjectMeta{
			Namespace: controlPlaneNamespace,
			Name:      "cloud-network-config-controller-creds",
		},
	}
}
