package manifests

import (
	hyperv1 "github.com/openshift/hypershift/api/hypershift/v1beta1"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var RbacCapabilityMap = map[string]hyperv1.OptionalCapability{
	"system:openshift:openshift-controller-manager:ingress-to-route-controller/ClusterRole":        hyperv1.IngressCapability,
	"openshift-route-controller-manager/openshift-route-controllers/Role":                          hyperv1.IngressCapability,
	"system:openshift:openshift-controller-manager:ingress-to-route-controller/ClusterRoleBinding": hyperv1.IngressCapability,
	"openshift-route-controller-manager/openshift-route-controllers/RoleBinding":                   hyperv1.IngressCapability,
	// add others as needed
}

func CSRApproverClusterRole() *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:openshift:controller:cluster-csr-approver-controller",
		},
	}
}

func CSRApproverClusterRoleBinding() *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:openshift:controller:cluster-csr-approver-controller",
		},
	}
}

func IngressToRouteControllerClusterRole() *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:openshift:openshift-controller-manager:ingress-to-route-controller",
		},
	}
}

func IngressToRouteControllerRole() *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "openshift-route-controller-manager",
			Name:      "openshift-route-controllers",
		},
	}
}

func IngressToRouteControllerClusterRoleBinding() *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:openshift:openshift-controller-manager:ingress-to-route-controller",
		},
	}
}

func IngressToRouteControllerRoleBinding() *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "openshift-route-controller-manager",
			Name:      "openshift-route-controllers",
		},
	}
}

func NamespaceSecurityAllocationControllerClusterRole() *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:openshift:controller:namespace-security-allocation-controller",
		},
	}
}

func NamespaceSecurityAllocationControllerClusterRoleBinding() *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:openshift:controller:namespace-security-allocation-controller",
		},
	}
}

func PodSecurityAdmissionLabelSyncerControllerClusterRole() *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:openshift:controller:podsecurity-admission-label-syncer-controller",
		},
	}
}

func PodSecurityAdmissionLabelSyncerControllerRoleBinding() *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:openshift:controller:podsecurity-admission-label-syncer-controller",
		},
	}
}

func PriviligedNamespacesPSALabelSyncerClusterRole() *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:openshift:controller:privileged-namespaces-psa-label-syncer",
		},
	}
}

func PriviligedNamespacesPSALabelSyncerClusterRoleBinding() *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:openshift:controller:privileged-namespaces-psa-label-syncer",
		},
	}
}

func NodeBootstrapperClusterRoleBinding() *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "create-csrs-for-bootstrapping",
		},
	}
}

func CSRRenewalClusterRoleBinding() *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system-bootstrap-node-renewal",
		},
	}
}

func MetricsClientClusterRoleBinding() *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "hypershift-metrics-client",
		},
	}
}

func AuthenticatedReaderForAuthenticatedUserRolebinding() *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "authentication-reader-for-authenticated-users",
			Namespace: "kube-system",
		}}
}

func KCMLeaderElectionRole() *rbacv1.Role {
	return &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "system:openshift:leader-election-lock-kube-controller-manager",
			Namespace: "kube-system",
		},
	}
}

func KCMLeaderElectionRoleBinding() *rbacv1.RoleBinding {
	return &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "system:openshift:leader-election-lock-kube-controller-manager",
			Namespace: "kube-system",
		},
	}
}

func DeployerClusterRole() *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:deployer",
		},
	}
}

func DeployerClusterRoleBinding() *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:deployer",
		},
	}
}

func ImageTriggerControllerClusterRole() *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:openshift:openshift-controller-manager:image-trigger-controller",
		},
	}
}

func ImageTriggerControllerClusterRoleBinding() *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:openshift:openshift-controller-manager:image-trigger-controller",
		},
	}
}

func UserOAuthClusterRole() *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:openshift:useroauthaccesstoken-manager",
		},
	}
}

func UserOAuthClusterRoleBinding() *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:openshift:useroauthaccesstoken-manager",
		},
	}
}

func AzureDiskCSIDriverNodeServiceAccountRole() *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:openshift:openshift-cluster-csi-drivers:azure-disk-csi-driver-node-sa",
		},
	}
}

func AzureDiskCSIDriverNodeServiceAccountRoleBinding() *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:openshift:openshift-cluster-csi-drivers:azure-disk-csi-driver-node-sa",
		},
	}
}

func AzureFileCSIDriverNodeServiceAccountRole() *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:serviceaccount:openshift-cluster-csi-drivers:azure-file-csi-driver-node-sa",
		},
	}
}

func AzureFileCSIDriverNodeServiceAccountRoleBinding() *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:serviceaccount:openshift-cluster-csi-drivers:azure-file-csi-driver-node-sa",
		},
	}
}

func CloudNetworkConfigControllerServiceAccountRole() *rbacv1.ClusterRole {
	return &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:serviceaccount:openshift-cloud-network-config-controller:cloud-network-config-controller",
		},
	}
}

func CloudNetworkConfigControllerServiceAccountRoleBinding() *rbacv1.ClusterRoleBinding {
	return &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name: "system:serviceaccount:openshift-cloud-network-config-controller:cloud-network-config-controller",
		},
	}
}
